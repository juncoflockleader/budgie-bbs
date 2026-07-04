package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/handler"
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
	var n sql.NullInt64
	err := qQueryRow(s.db, `SELECT SUM(count_value) FROM poll_vote_count_shards WHERE poll_id=? AND option_id=?`, pollID, optionID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n.Valid {
		return int(n.Int64), nil
	}
	var count int
	err = qQueryRow(s.db, `SELECT COUNT(*) FROM poll_votes WHERE poll_id=? AND option_id=?`, pollID, optionID).Scan(&count)
	return count, err
}

func (s sqlCounterStore) PollVote(pollID, userID string) (string, bool, error) {
	var optionID string
	err := qQueryRow(s.db, `SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, userID).Scan(&optionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return optionID, true, nil
}

func (s sqlCounterStore) UserCounterIdentity(userID string) (CounterUserIdentity, error) {
	identity := CounterUserIdentity{}
	reactionRows, err := qQuery(s.db, `SELECT post_id, user_id, emoji, ts FROM post_reactions WHERE user_id=? ORDER BY post_id`, userID)
	if err != nil {
		return identity, err
	}
	for reactionRows.Next() {
		var row CounterReactionIdentity
		if err := reactionRows.Scan(&row.PostID, &row.UserID, &row.Emoji, &row.TS); err != nil {
			_ = reactionRows.Close()
			return identity, err
		}
		identity.Reactions = append(identity.Reactions, row)
	}
	if err := reactionRows.Err(); err != nil {
		_ = reactionRows.Close()
		return identity, err
	}
	if err := reactionRows.Close(); err != nil {
		return identity, err
	}

	voteRows, err := qQuery(s.db, `SELECT poll_id, option_id, user_id, ts FROM poll_votes WHERE user_id=? ORDER BY poll_id`, userID)
	if err != nil {
		return identity, err
	}
	for voteRows.Next() {
		var row CounterPollVoteIdentity
		if err := voteRows.Scan(&row.PollID, &row.OptionID, &row.UserID, &row.TS); err != nil {
			_ = voteRows.Close()
			return identity, err
		}
		identity.PollVotes = append(identity.PollVotes, row)
	}
	if err := voteRows.Err(); err != nil {
		_ = voteRows.Close()
		return identity, err
	}
	if err := voteRows.Close(); err != nil {
		return identity, err
	}
	return identity, nil
}

func (s sqlCounterStore) BeginMutation() (handler.CounterMutation, error) {
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
	_, err := qExec(m.tx, `UPDATE user_activity SET reactions_recv=0 WHERE user_id=?`, userID)
	return err
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
