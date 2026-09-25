package handler

import "github.com/juncoflockleader/budgie-bbs/internal/core/commandparse"

// pollBlock represents a parsed [poll] block from post markup.
type pollBlock struct {
	question  string
	options   []string
	expiresAt int64
}

func parseMentions(body string) []string {
	return commandparse.ParseMentions(body)
}

// ParseMentions extracts mentions without mutating handler internals.
func ParseMentions(body string) []string {
	return parseMentions(body)
}

func extractPoll(body string) (*pollBlock, string) {
	pb, cleanBody := commandparse.ParsePoll(body)
	if pb == nil {
		return nil, cleanBody
	}
	return &pollBlock{question: pb.Question, options: pb.Options, expiresAt: pb.ExpiresAt}, cleanBody
}

type PollBlock = commandparse.PollBlock

// ParsePoll converts the internal parser output into a stable public shape.
func ParsePoll(body string) (*PollBlock, string) {
	return commandparse.ParsePoll(body)
}

// ParsePollExpires is exported for compatibility callers.
func ParsePollExpires(raw string) (int64, error) {
	return commandparse.ParsePollExpires(raw)
}
