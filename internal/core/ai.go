package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

// Generative-AI integration accessors. The board api_token is write-only at the
// API boundary: BoardAIConfig (used by HTTP handlers) never carries it, while
// BoardAIRuntime (used by the responder processor) does and is never serialized.

// AISettings returns the site-wide AI toggle.
func (c *Core) AISettings() (*projections.AISettings, error) {
	return projections.GetAISettings(c.DB)
}

// SetAISettings flips the site-wide AI toggle (admin-only at the HTTP layer).
func (c *Core) SetAISettings(enabled bool) (*projections.AISettings, error) {
	return projections.SetAISettings(c.DB, enabled)
}

// AIEnabled reports whether AI is enabled site-wide. Errors are treated as
// disabled so a storage hiccup never silently activates AI.
func (c *Core) AIEnabled() bool {
	s, err := projections.GetAISettings(c.DB)
	return err == nil && s != nil && s.Enabled
}

// BoardAIConfig returns the API-safe (token-free) config for a board.
func (c *Core) BoardAIConfig(boardID string) (*projections.BoardAIConfig, error) {
	return projections.GetBoardAIConfig(c.DB, boardID)
}

// SetBoardAIConfig applies a partial patch and returns the API-safe config.
func (c *Core) SetBoardAIConfig(boardID string, patch projections.BoardAIConfigPatch) (*projections.BoardAIConfig, error) {
	return projections.SetBoardAIConfig(c.DB, boardID, patch)
}

// BoardAIRuntime returns the full server-side config (incl. token + counters)
// for the responder processor. nil when no config exists.
func (c *Core) BoardAIRuntime(boardID string) (*projections.BoardAIRuntime, error) {
	return projections.GetBoardAIRuntime(c.DB, boardID)
}

// EnsureBoardAIBot creates (or finds) the board's AI bot account, forces it
// approved, makes it a board member so it can post even in member-post boards,
// and records its id in the board's AI config. Idempotent.
func (c *Core) EnsureBoardAIBot(boardID string) (*projections.User, error) {
	if rt, err := c.BoardAIRuntime(boardID); err == nil && rt != nil && rt.BotUserID != "" {
		if u, err := c.UserByID(rt.BotUserID); err == nil && u != nil {
			return u, c.activateBoardAIBot(boardID, u.ID)
		}
	}
	botName := accountmodel.BoardAIBotName(boardID)
	if u, err := c.UserByName(botName); err == nil && u != nil {
		return u, c.activateBoardAIBot(boardID, u.ID)
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	u, err := c.registerUserInternal(botName, hex.EncodeToString(pw))
	if err != nil {
		return nil, fmt.Errorf("create AI bot user: %w", err)
	}
	if err := c.activateBoardAIBot(boardID, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

// activateBoardAIBot forces the bot approved, ensures board membership, and
// stores the bot id on the config.
func (c *Core) activateBoardAIBot(boardID, botUserID string) error {
	ts := nowMS()
	if _, err := qExec(c.DB, `UPDATE users SET registration_status='approved' WHERE id=?`, botUserID); err != nil {
		return err
	}
	if _, err := qExec(c.DB,
		`INSERT INTO board_members (board_id, user_id, title, created_at, updated_at)
		 VALUES (?, ?, 'AI bot', ?, ?)
		 ON CONFLICT(board_id, user_id) DO NOTHING`,
		boardID, botUserID, ts, ts); err != nil {
		return err
	}
	return projections.SetBoardAIBotUser(c.DB, boardID, botUserID)
}
