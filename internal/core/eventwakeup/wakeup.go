package eventwakeup

import (
	"encoding/json"
)

type PostReaction struct {
	Post  string `json:"post"`
	User  string `json:"user"`
	Emoji string `json:"emoji,omitempty"`
	TS    int64  `json:"ts"`
}

func EncodePostReaction(postID, userID, emoji string, ts int64) string {
	raw, err := json.Marshal(PostReaction{Post: postID, User: userID, Emoji: emoji, TS: ts})
	if err != nil {
		return ""
	}
	return string(raw)
}

type PollVote struct {
	Poll string `json:"poll"`
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

func EncodePollVote(pollID, userID string, ts int64) string {
	raw, err := json.Marshal(PollVote{Poll: pollID, User: userID, TS: ts})
	if err != nil {
		return ""
	}
	return string(raw)
}
