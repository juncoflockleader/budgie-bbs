package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type sqlCounterStore struct {
	db *sql.DB
}

func (s sqlCounterStore) UserReacted(postID, userID string) (bool, error) {
	return projections.UserReacted(s.db, postID, userID)
}

func (s sqlCounterStore) ReactionCount(postID string) (int, error) {
	return projections.ReactionCount(s.db, postID)
}

func (s sqlCounterStore) PollOptionVoteCount(pollID, optionID string) (int, error) {
	return counterstore.PollOptionVoteCount(s.db, pollID, optionID)
}

func (s sqlCounterStore) PollVote(pollID, userID string) (string, bool, error) {
	return counterstore.PollVote(s.db, pollID, userID)
}

func (s sqlCounterStore) UserCounterIdentity(userID string) (counterstore.UserIdentity, error) {
	return counterstore.UserCounterIdentity(s.db, userID)
}

func (s sqlCounterStore) BeginMutation() (counterstore.Mutation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	return &sqlCounterMutation{tx: tx}, nil
}

type sqlCounterMutation struct {
	tx     *sql.Tx
	closed bool
}

func (m *sqlCounterMutation) UpsertReaction(postID, userID, emoji string, ts int64) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return projections.UpsertReaction(m.tx, postID, userID, emoji, ts)
}

func (m *sqlCounterMutation) DeleteReaction(postID, userID string) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return projections.DeleteReaction(m.tx, postID, userID)
}

func (m *sqlCounterMutation) ReactionCount(postID string) (int, error) {
	if m == nil || m.tx == nil {
		return 0, sql.ErrTxDone
	}
	return projections.ReactionCountTx(m.tx, postID)
}

func (m *sqlCounterMutation) CastVote(pollID, optionID, userID string, ts int64) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return projections.CastVote(m.tx, pollID, optionID, userID, ts)
}

func (m *sqlCounterMutation) DeletePollVote(pollID, userID string) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return projections.DeletePollVote(m.tx, pollID, userID)
}

func (m *sqlCounterMutation) RecordReactionReceived(postAuthorID string) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return recordReactionReceivedTx(m.tx, postAuthorID)
}

func (m *sqlCounterMutation) RecordReactionRemoved(postAuthorID string) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return recordReactionRemovedTx(m.tx, postAuthorID)
}

func (m *sqlCounterMutation) ClearReactionReceived(userID string) error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	return counterstore.ClearReactionReceived(m.tx, userID)
}

func (m *sqlCounterMutation) Commit() error {
	if m == nil || m.tx == nil {
		return sql.ErrTxDone
	}
	if m.closed {
		return sql.ErrTxDone
	}
	m.closed = true
	return m.tx.Commit()
}

func (m *sqlCounterMutation) Rollback() error {
	if m == nil || m.tx == nil || m.closed {
		return nil
	}
	m.closed = true
	return m.tx.Rollback()
}
