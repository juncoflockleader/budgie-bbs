package counterstore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func PollOptionVoteCount(db *sql.DB, pollID, optionID string) (int, error) {
	var n sql.NullInt64
	err := sqlstore.QueryRow(db, `SELECT SUM(count_value) FROM poll_vote_count_shards WHERE poll_id=? AND option_id=?`, pollID, optionID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n.Valid {
		return int(n.Int64), nil
	}
	var count int
	err = sqlstore.QueryRow(db, `SELECT COUNT(*) FROM poll_votes WHERE poll_id=? AND option_id=?`, pollID, optionID).Scan(&count)
	return count, err
}

func PollVote(db *sql.DB, pollID, userID string) (string, bool, error) {
	var optionID string
	err := sqlstore.QueryRow(db, `SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, userID).Scan(&optionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return optionID, true, nil
}

func UserCounterIdentity(queryable sqlstore.SQLLike, userID string) (UserIdentity, error) {
	identity := UserIdentity{}
	reactionRows, err := sqlstore.Query(queryable, `SELECT post_id, user_id, emoji, ts FROM post_reactions WHERE user_id=? ORDER BY post_id`, userID)
	if err != nil {
		return identity, err
	}
	for reactionRows.Next() {
		var row ReactionIdentity
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

	voteRows, err := sqlstore.Query(queryable, `SELECT poll_id, option_id, user_id, ts FROM poll_votes WHERE user_id=? ORDER BY poll_id`, userID)
	if err != nil {
		return identity, err
	}
	for voteRows.Next() {
		var row PollVoteIdentity
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

func ReactionAuthors(queryable sqlstore.RowQueryable, reactions []ReactionIdentity) (map[string]string, error) {
	authors := map[string]string{}
	for _, reaction := range reactions {
		if reaction.PostID == "" {
			continue
		}
		if _, ok := authors[reaction.PostID]; ok {
			continue
		}
		var authorID string
		err := sqlstore.QueryRow(queryable, `SELECT author_id FROM posts WHERE id=?`, reaction.PostID).Scan(&authorID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		authors[reaction.PostID] = authorID
	}
	return authors, nil
}

func ClearReactionReceived(tx *sql.Tx, userID string) error {
	_, err := sqlstore.Exec(tx, `UPDATE user_activity SET reactions_recv=0 WHERE user_id=?`, userID)
	return err
}
