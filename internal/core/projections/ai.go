package projections

import (
	"database/sql"
	"strings"
)

// Generative-AI integration: a site-wide kill switch (ai_settings) and per-board
// bot configuration (board_ai_config). The board's bring-your-own api_token is
// deliberately never surfaced through any read API — GetBoardAIConfig omits it
// and only reports whether one is set. Only GetBoardAIRuntime (server-side, for
// the responder processor) returns the token, and it is never serialized.

// AISettings is the site-wide AI toggle.
type AISettings struct {
	Enabled   bool  `json:"enabled"`
	UpdatedAt int64 `json:"updatedAt"`
}

// BoardAIConfig is the API-safe view of a board's AI bot config. It never
// carries the api_token; TokenSet reports whether one has been stored.
type BoardAIConfig struct {
	BoardID     string `json:"boardId"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	TokenSet    bool   `json:"tokenSet"`
	TriggerRole string `json:"triggerRole"` // "user" (any author) | "mod"
	Mode        string `json:"mode"`        // "every_post" | "reply"
	ReplyPrompt string `json:"replyPrompt"`
	MaxTotal    int    `json:"maxTotal"`
	MaxPerHour  int    `json:"maxPerHour"`
	UsedTotal   int    `json:"usedTotal"`
	BotUserID   string `json:"botUserId"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// BoardAIConfigPatch carries partial updates. A nil field is left unchanged; for
// APIToken specifically, nil leaves the stored token untouched while "" clears it.
type BoardAIConfigPatch struct {
	Enabled     *bool
	Provider    *string
	Model       *string
	APIToken    *string
	TriggerRole *string
	Mode        *string
	ReplyPrompt *string
	MaxTotal    *int
	MaxPerHour  *int
}

// BoardAIRuntime is the full server-side row, including the secret token and
// usage counters. It is used only by the responder processor and is never
// returned to clients.
type BoardAIRuntime struct {
	BoardID     string
	Enabled     bool
	Provider    string
	Model       string
	APIToken    string
	TriggerRole string
	Mode        string
	ReplyPrompt string
	MaxTotal    int
	MaxPerHour  int
	UsedTotal   int
	WindowStart int64
	WindowCount int
	BotUserID   string
}

// NormalizeAITriggerRole canonicalizes the trigger-role to "user" or "mod".
func NormalizeAITriggerRole(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == "mod" {
		return "mod"
	}
	return "user"
}

// NormalizeAIMode canonicalizes the mode to "every_post" or "reply".
func NormalizeAIMode(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == "every_post" {
		return "every_post"
	}
	return "reply"
}

func GetAISettings(db *sql.DB) (*AISettings, error) {
	out := &AISettings{}
	var enabled int
	err := QQueryRow(db, `SELECT enabled, updated_at FROM ai_settings WHERE id='default'`).Scan(&enabled, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Enabled = enabled != 0
	return out, nil
}

func SetAISettings(db *sql.DB, enabled bool) (*AISettings, error) {
	_, err := QExec(db,
		`INSERT INTO ai_settings (id, enabled, updated_at) VALUES ('default', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled, updated_at=excluded.updated_at`,
		boolInt(enabled), NowMS())
	if err != nil {
		return nil, err
	}
	return GetAISettings(db)
}

// GetBoardAIRuntime returns the full row (incl. token + counters) or nil if no
// config exists for the board. Server-side only.
func GetBoardAIRuntime(db *sql.DB, boardID string) (*BoardAIRuntime, error) {
	r := &BoardAIRuntime{BoardID: boardID}
	var enabled int
	err := QQueryRow(db,
		`SELECT enabled, provider, model, api_token, trigger_role, mode, reply_prompt,
		        max_total, max_per_hour, used_total, window_start, window_count, bot_user_id
		   FROM board_ai_config WHERE board_id=?`, boardID,
	).Scan(&enabled, &r.Provider, &r.Model, &r.APIToken, &r.TriggerRole, &r.Mode, &r.ReplyPrompt,
		&r.MaxTotal, &r.MaxPerHour, &r.UsedTotal, &r.WindowStart, &r.WindowCount, &r.BotUserID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return r, nil
}

// GetBoardAIConfig returns the API-safe config (no token) for a board, or a
// zero-value default when none is stored.
func GetBoardAIConfig(db *sql.DB, boardID string) (*BoardAIConfig, error) {
	r, err := GetBoardAIRuntime(db, boardID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &BoardAIConfig{BoardID: boardID, Provider: "anthropic", Model: "claude-haiku-4-5", TriggerRole: "user", Mode: "reply"}, nil
	}
	var updatedAt int64
	_ = QQueryRow(db, `SELECT updated_at FROM board_ai_config WHERE board_id=?`, boardID).Scan(&updatedAt)
	return &BoardAIConfig{
		BoardID:     boardID,
		Enabled:     r.Enabled,
		Provider:    r.Provider,
		Model:       r.Model,
		TokenSet:    strings.TrimSpace(r.APIToken) != "",
		TriggerRole: r.TriggerRole,
		Mode:        r.Mode,
		ReplyPrompt: r.ReplyPrompt,
		MaxTotal:    r.MaxTotal,
		MaxPerHour:  r.MaxPerHour,
		UsedTotal:   r.UsedTotal,
		BotUserID:   r.BotUserID,
		UpdatedAt:   updatedAt,
	}, nil
}

// SetBoardAIConfig applies a partial patch (read-modify-write for portability)
// and returns the API-safe config. The api_token column changes only when
// patch.APIToken is non-nil.
func SetBoardAIConfig(db *sql.DB, boardID string, patch BoardAIConfigPatch) (*BoardAIConfig, error) {
	cur, err := GetBoardAIRuntime(db, boardID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		cur = &BoardAIRuntime{BoardID: boardID, Provider: "anthropic", Model: "claude-haiku-4-5", TriggerRole: "user", Mode: "reply"}
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}
	if patch.Provider != nil && strings.TrimSpace(*patch.Provider) != "" {
		cur.Provider = strings.TrimSpace(*patch.Provider)
	}
	if patch.Model != nil && strings.TrimSpace(*patch.Model) != "" {
		cur.Model = strings.TrimSpace(*patch.Model)
	}
	if patch.APIToken != nil {
		cur.APIToken = strings.TrimSpace(*patch.APIToken)
	}
	if patch.TriggerRole != nil {
		cur.TriggerRole = NormalizeAITriggerRole(*patch.TriggerRole)
	}
	if patch.Mode != nil {
		cur.Mode = NormalizeAIMode(*patch.Mode)
	}
	if patch.ReplyPrompt != nil {
		cur.ReplyPrompt = *patch.ReplyPrompt
	}
	if patch.MaxTotal != nil && *patch.MaxTotal >= 0 {
		cur.MaxTotal = *patch.MaxTotal
	}
	if patch.MaxPerHour != nil && *patch.MaxPerHour >= 0 {
		cur.MaxPerHour = *patch.MaxPerHour
	}
	if _, err := QExec(db,
		`INSERT INTO board_ai_config
		   (board_id, enabled, provider, model, api_token, trigger_role, mode, reply_prompt,
		    max_total, max_per_hour, used_total, window_start, window_count, bot_user_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(board_id) DO UPDATE SET
		    enabled=excluded.enabled, provider=excluded.provider, model=excluded.model,
		    api_token=excluded.api_token, trigger_role=excluded.trigger_role, mode=excluded.mode,
		    reply_prompt=excluded.reply_prompt, max_total=excluded.max_total,
		    max_per_hour=excluded.max_per_hour, bot_user_id=excluded.bot_user_id,
		    updated_at=excluded.updated_at`,
		boardID, boolInt(cur.Enabled), cur.Provider, cur.Model, cur.APIToken, cur.TriggerRole, cur.Mode,
		cur.ReplyPrompt, cur.MaxTotal, cur.MaxPerHour, cur.UsedTotal, cur.WindowStart, cur.WindowCount,
		cur.BotUserID, NowMS()); err != nil {
		return nil, err
	}
	return GetBoardAIConfig(db, boardID)
}

// SetBoardAIBotUser records the bot user id for a board (set once the bot user
// is created on enable).
func SetBoardAIBotUser(db *sql.DB, boardID, botUserID string) error {
	_, err := QExec(db, `UPDATE board_ai_config SET bot_user_id=?, updated_at=? WHERE board_id=?`,
		botUserID, NowMS(), boardID)
	return err
}

// RecordBoardAIUsage increments the usage counters after a successful
// generation, rolling the per-hour window when it has elapsed.
func RecordBoardAIUsage(db *sql.DB, boardID string, nowMS int64) error {
	r, err := GetBoardAIRuntime(db, boardID)
	if err != nil || r == nil {
		return err
	}
	windowStart := r.WindowStart
	windowCount := r.WindowCount
	if nowMS-windowStart >= 3600_000 {
		windowStart = nowMS
		windowCount = 0
	}
	windowCount++
	_, err = QExec(db,
		`UPDATE board_ai_config SET used_total=used_total+1, window_start=?, window_count=?, updated_at=? WHERE board_id=?`,
		windowStart, windowCount, nowMS, boardID)
	return err
}
