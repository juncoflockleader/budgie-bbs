package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/aiprovider"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// aiResponderView is the watermark key tracking how far the AI responder has
// consumed the durable event log.
const aiResponderView = "ai.responder"

// aiGenerateFunc generates a reply for a board's bot. Injectable so tests can
// avoid real network calls.
type aiGenerateFunc func(ctx context.Context, rt *BoardAIRuntime, system, prompt string) (string, error)

func defaultAIGenerate(ctx context.Context, rt *BoardAIRuntime, system, prompt string) (string, error) {
	return aiprovider.Generate(ctx, aiprovider.Request{
		Provider:  rt.Provider,
		Model:     rt.Model,
		APIToken:  rt.APIToken,
		System:    system,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
}

// AIProcessor consumes post.appended events and, when a board has an enabled AI
// bot, generates and posts a reply as that bot. LLM calls are slow, so this runs
// off the command path with a small batch.
type AIProcessor struct {
	core      *Core
	BatchSize int
	Interval  time.Duration
	Generate  aiGenerateFunc
}

func NewAIProcessor(c *Core, interval time.Duration, batchSize int) (*AIProcessor, error) {
	if c == nil {
		return nil, fmt.Errorf("ai processor: nil core")
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 5
	}
	return &AIProcessor{core: c, BatchSize: batchSize, Interval: interval, Generate: defaultAIGenerate}, nil
}

// StartAIResponderProcessor launches the AI responder in the background.
func (c *Core) StartAIResponderProcessor(ctx context.Context, interval time.Duration, batchSize int) (*AIProcessor, error) {
	p, err := NewAIProcessor(c, interval, batchSize)
	if err != nil {
		return nil, err
	}
	go p.Run(ctx)
	return p, nil
}

func (p *AIProcessor) Run(ctx context.Context) {
	drain := func() {
		for ctx.Err() == nil {
			res, err := p.ProcessOnce(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("ai responder processor failed", "err", err)
				}
				return
			}
			if res.Events < p.BatchSize || res.AppliedSeq >= res.HeadSeq {
				return
			}
		}
	}
	drain()
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drain()
		}
	}
}

// AIProcessResult reports a single drain pass.
type AIProcessResult struct {
	FromSeq    int64
	AppliedSeq int64
	HeadSeq    int64
	Events     int
	Replied    int
}

// ProcessOnce advances the responder watermark by one batch, replying via the
// bot where a board's config calls for it. The watermark always advances past
// processed events (even when no reply is made or generation fails) so a single
// bad post never stalls the stream and a later re-enable doesn't replay history.
func (p *AIProcessor) ProcessOnce(ctx context.Context) (AIProcessResult, error) {
	c := p.core
	fromSeq, _, err := lookupDerivedViewAppliedSeq(c.DB, aiResponderView)
	if err != nil {
		return AIProcessResult{}, err
	}
	head, err := c.Head()
	if err != nil {
		return AIProcessResult{}, err
	}
	res := AIProcessResult{FromSeq: fromSeq, AppliedSeq: fromSeq, HeadSeq: head}
	events, err := c.Replay(fromSeq, nil, p.BatchSize)
	if err != nil {
		return res, err
	}
	siteEnabled := c.AIEnabled()
	for _, evt := range events {
		if evt == nil {
			continue
		}
		res.Events++
		if evt.Seq > res.AppliedSeq {
			res.AppliedSeq = evt.Seq
		}
		if !siteEnabled || evt.Kind != proto.EvtPostAppended {
			continue
		}
		payload, ok := evt.Payload.(*proto.PostAppendedPayload)
		if !ok {
			continue
		}
		if p.maybeReply(ctx, payload) {
			res.Replied++
		}
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback() //nolint
	if err := recordDerivedViewAppliedTx(tx, aiResponderView, res.AppliedSeq); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}

// maybeReply evaluates one post against its board's AI config and, when it
// qualifies, generates and posts a reply as the bot. Returns true if it replied.
func (p *AIProcessor) maybeReply(ctx context.Context, post *proto.PostAppendedPayload) bool {
	c := p.core
	if strings.TrimSpace(post.AuthorID) == "" {
		return false // anonymous/system posts have no triggering author
	}
	thread, err := c.GetThread(post.Thread)
	if err != nil || thread == nil {
		return false
	}
	rt, err := c.BoardAIRuntime(thread.Board)
	if err != nil || rt == nil || !rt.Enabled || strings.TrimSpace(rt.APIToken) == "" || rt.BotUserID == "" {
		return false
	}
	if post.AuthorID == rt.BotUserID {
		return false // never respond to our own bot (loop guard)
	}

	// Permission: "mod" responds only to posts by a board/site moderator.
	if rt.TriggerRole == "mod" {
		author, err := c.UserByID(post.AuthorID)
		if err != nil || author == nil {
			return false
		}
		info, err := c.GetBoardInfo(thread.Board)
		if err != nil || info == nil || !actorModeratesBoard(author, info) {
			return false
		}
	}

	// Behavior: "reply" responds only when the post is a reply to the bot.
	if rt.Mode == "reply" {
		if post.ReplyTo == "" {
			return false
		}
		target, err := c.GetPost(post.ReplyTo)
		if err != nil || target == nil || target.AuthorID != rt.BotUserID {
			return false
		}
	}

	// Usage limits.
	now := nowMS()
	if rt.MaxTotal > 0 && rt.UsedTotal >= rt.MaxTotal {
		return false
	}
	if rt.MaxPerHour > 0 && now-rt.WindowStart < 3600_000 && rt.WindowCount >= rt.MaxPerHour {
		return false
	}

	botUser, err := c.UserByID(rt.BotUserID)
	if err != nil || botUser == nil {
		return false
	}

	system, prompt := p.buildPrompt(rt, botUser.Name, thread, post)
	text, err := p.Generate(ctx, rt, system, prompt)
	if err != nil {
		slog.Warn("ai responder generation failed", "board", thread.Board, "err", err)
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	payload, _ := json.Marshal(proto.AppendPostPayload{Thread: post.Thread, Body: text, ReplyTo: post.ID})
	reply := c.ExecCmd(ctx, botUser, proto.CmdAppendPost, payload, newID("aicmd_"))
	if reply.Err != nil {
		slog.Warn("ai responder post failed", "board", thread.Board, "err", reply.Err.Message)
		return false
	}
	if err := projections.RecordBoardAIUsage(c.DB, thread.Board, now); err != nil {
		slog.Warn("ai responder usage record failed", "board", thread.Board, "err", err)
	}
	slog.Info("ai responder replied", "board", thread.Board, "thread", post.Thread, "bot", botUser.Name)
	return true
}

// buildPrompt assembles the system prompt (board-mod reply prompt + a default
// instruction) and a transcript of the recent thread for the user message.
func (p *AIProcessor) buildPrompt(rt *BoardAIRuntime, botName string, thread *Thread, triggering *proto.PostAppendedPayload) (system, prompt string) {
	var sys strings.Builder
	fmt.Fprintf(&sys, "You are %q, an AI participant on the %q board of a community forum. ", botName, thread.Board)
	sys.WriteString("Write a single concise, helpful forum reply to the most recent message. Do not include a salutation or signature.")
	if rp := strings.TrimSpace(rt.ReplyPrompt); rp != "" {
		sys.WriteString("\n\nAdditional instructions from the board moderators:\n")
		sys.WriteString(rp)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Thread title: %s\n\nRecent messages:\n", thread.Title)
	posts, _ := p.core.ListPosts(thread.ID, 12, 0)
	for _, post := range posts {
		body := strings.TrimSpace(post.Body)
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", post.Author, body)
	}
	fmt.Fprintf(&b, "\nReply to the latest message from %s:\n%s", triggering.Author, strings.TrimSpace(triggering.Body))
	return sys.String(), b.String()
}
