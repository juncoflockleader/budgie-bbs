package counterstore

// Store owns high-fanout unordered counters and identity markers. The default
// implementation is SQL-backed, but command handlers only depend on this
// boundary so internet-scale counter shards can replace it independently.
type Store interface {
	UserReacted(postID, userID string) (bool, error)
	ReactionCount(postID string) (int, error)
	PollOptionVoteCount(pollID, optionID string) (int, error)
	PollVote(pollID, userID string) (string, bool, error)
	UserCounterIdentity(userID string) (UserIdentity, error)
	BeginMutation() (Mutation, error)
}

type UserIdentity struct {
	Reactions []ReactionIdentity
	PollVotes []PollVoteIdentity
}

type ReactionIdentity struct {
	PostID string
	UserID string
	Emoji  string
	TS     int64
}

type PollVoteIdentity struct {
	PollID   string
	OptionID string
	UserID   string
	TS       int64
}

type Mutation interface {
	UpsertReaction(postID, userID, emoji string, ts int64) error
	DeleteReaction(postID, userID string) error
	ReactionCount(postID string) (int, error)
	CastVote(pollID, optionID, userID string, ts int64) error
	DeletePollVote(pollID, userID string) error
	RecordReactionReceived(postAuthorID string) error
	RecordReactionRemoved(postAuthorID string) error
	ClearReactionReceived(userID string) error
	Commit() error
	Rollback() error
}
