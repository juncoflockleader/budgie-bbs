package core

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	counterKindPostReactions   = "post.reactions"
	counterKindPollOptionVotes = "poll.option_votes"
)

// CheckpointCounters appends a durable coarse snapshot for unordered counter
// storage. It is a low-frequency recovery anchor; per-click reaction and vote
// writes remain outside the ordered event log.
func (c *Core) CheckpointCounters(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint

	sourceHead, err := headSeqTx(tx)
	if err != nil {
		return 0, err
	}
	ts := nowMS()
	var payload *proto.CounterCheckpointPayload
	if c.useCounterStoreOverride() {
		payload, err = buildCounterCheckpointPayloadFromCounterStore(tx, sourceHead, ts, c.counterStore)
	} else {
		payload, err = buildCounterCheckpointPayload(tx, sourceHead, ts)
	}
	if err != nil {
		return 0, err
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtCounterCheckpointed, []string{"counter:unordered"}, payload)
	if err != nil {
		return 0, err
	}
	if err := recordCounterCheckpointTx(tx, seq, payload); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

func headSeqTx(tx *sql.Tx) (int64, error) {
	var head sql.NullInt64
	if err := qQueryRow(tx, `SELECT MAX(seq) FROM events`).Scan(&head); err != nil {
		return 0, err
	}
	return head.Int64, nil
}

func buildCounterCheckpointPayload(tx *sql.Tx, sourceHeadSeq, ts int64) (*proto.CounterCheckpointPayload, error) {
	payload := &proto.CounterCheckpointPayload{
		Complete:      true,
		SourceHeadSeq: sourceHeadSeq,
		TS:            ts,
	}

	postRows, err := qQuery(tx,
		`SELECT p.id,
		        COALESCE(
		          (SELECT SUM(prcs.count_value) FROM post_reaction_count_shards prcs WHERE prcs.post_id=p.id),
		          COUNT(pr.user_id),
		          0
		        ) AS reaction_count
		   FROM posts p
		   LEFT JOIN post_reactions pr ON pr.post_id=p.id
		  GROUP BY p.id
		  ORDER BY p.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("build post reaction counter checkpoint: %w", err)
	}
	for postRows.Next() {
		var row proto.PostReactionCounterCheckpoint
		if err := postRows.Scan(&row.PostID, &row.Count); err != nil {
			postRows.Close()
			return nil, fmt.Errorf("build post reaction counter checkpoint: %w", err)
		}
		payload.PostReactions = append(payload.PostReactions, row)
	}
	if err := postRows.Err(); err != nil {
		postRows.Close()
		return nil, fmt.Errorf("build post reaction counter checkpoint: %w", err)
	}
	postRows.Close()

	voteRows, err := qQuery(tx,
		`SELECT po.poll_id, po.id,
		        COALESCE(
		          (SELECT SUM(pvcs.count_value) FROM poll_vote_count_shards pvcs WHERE pvcs.option_id=po.id),
		          COUNT(pv.user_id),
		          0
		        ) AS vote_count
		   FROM poll_options po
		   LEFT JOIN poll_votes pv ON pv.option_id=po.id
		  GROUP BY po.poll_id, po.id
		  ORDER BY po.poll_id, po.position, po.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("build poll vote counter checkpoint: %w", err)
	}
	for voteRows.Next() {
		var row proto.PollOptionVoteCounterCheckpoint
		if err := voteRows.Scan(&row.PollID, &row.OptionID, &row.Count); err != nil {
			voteRows.Close()
			return nil, fmt.Errorf("build poll vote counter checkpoint: %w", err)
		}
		payload.PollOptionVotes = append(payload.PollOptionVotes, row)
	}
	if err := voteRows.Err(); err != nil {
		voteRows.Close()
		return nil, fmt.Errorf("build poll vote counter checkpoint: %w", err)
	}
	voteRows.Close()
	return payload, nil
}

func buildCounterCheckpointPayloadFromCounterStore(tx *sql.Tx, sourceHeadSeq, ts int64, store CounterStore) (*proto.CounterCheckpointPayload, error) {
	if store == nil {
		return nil, fmt.Errorf("counter store checkpoint: nil store")
	}
	payload := &proto.CounterCheckpointPayload{
		Complete:      true,
		SourceHeadSeq: sourceHeadSeq,
		TS:            ts,
	}

	postRows, err := qQuery(tx, `SELECT id FROM posts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("build post reaction counter checkpoint from counter store: %w", err)
	}
	for postRows.Next() {
		var postID string
		if err := postRows.Scan(&postID); err != nil {
			postRows.Close()
			return nil, fmt.Errorf("build post reaction counter checkpoint from counter store: %w", err)
		}
		count, err := store.ReactionCount(postID)
		if err != nil {
			postRows.Close()
			return nil, fmt.Errorf("read post reaction counter %s: %w", postID, err)
		}
		payload.PostReactions = append(payload.PostReactions, proto.PostReactionCounterCheckpoint{
			PostID: postID,
			Count:  count,
		})
	}
	if err := postRows.Err(); err != nil {
		postRows.Close()
		return nil, fmt.Errorf("build post reaction counter checkpoint from counter store: %w", err)
	}
	postRows.Close()

	voteRows, err := qQuery(tx, `SELECT poll_id, id FROM poll_options ORDER BY poll_id, position, id`)
	if err != nil {
		return nil, fmt.Errorf("build poll vote counter checkpoint from counter store: %w", err)
	}
	for voteRows.Next() {
		var pollID, optionID string
		if err := voteRows.Scan(&pollID, &optionID); err != nil {
			voteRows.Close()
			return nil, fmt.Errorf("build poll vote counter checkpoint from counter store: %w", err)
		}
		count, err := store.PollOptionVoteCount(pollID, optionID)
		if err != nil {
			voteRows.Close()
			return nil, fmt.Errorf("read poll option vote counter %s/%s: %w", pollID, optionID, err)
		}
		payload.PollOptionVotes = append(payload.PollOptionVotes, proto.PollOptionVoteCounterCheckpoint{
			PollID:   pollID,
			OptionID: optionID,
			Count:    count,
		})
	}
	if err := voteRows.Err(); err != nil {
		voteRows.Close()
		return nil, fmt.Errorf("build poll vote counter checkpoint from counter store: %w", err)
	}
	voteRows.Close()
	return payload, nil
}

func recordCounterCheckpointTx(tx *sql.Tx, checkpointSeq int64, payload *proto.CounterCheckpointPayload) error {
	if payload == nil {
		return nil
	}
	if payload.Complete {
		if _, err := qExec(tx, `DELETE FROM counter_checkpoints`); err != nil {
			return fmt.Errorf("clear complete counter checkpoint: %w", err)
		}
	}
	for _, row := range payload.PostReactions {
		if _, err := qExec(tx,
			`INSERT INTO counter_checkpoints (
			    counter_kind, target_id, parent_id, count, source_head_seq, checkpoint_seq, checkpointed_at
			 ) VALUES (?,?,?,?,?,?,?)
			 ON CONFLICT(counter_kind, target_id) DO UPDATE SET
			    parent_id=excluded.parent_id,
			    count=excluded.count,
			    source_head_seq=excluded.source_head_seq,
			    checkpoint_seq=excluded.checkpoint_seq,
			    checkpointed_at=excluded.checkpointed_at`,
			counterKindPostReactions, row.PostID, "", row.Count, payload.SourceHeadSeq, checkpointSeq, payload.TS,
		); err != nil {
			return fmt.Errorf("record post reaction counter checkpoint %s: %w", row.PostID, err)
		}
	}
	for _, row := range payload.PollOptionVotes {
		if _, err := qExec(tx,
			`INSERT INTO counter_checkpoints (
			    counter_kind, target_id, parent_id, count, source_head_seq, checkpoint_seq, checkpointed_at
			 ) VALUES (?,?,?,?,?,?,?)
			 ON CONFLICT(counter_kind, target_id) DO UPDATE SET
			    parent_id=excluded.parent_id,
			    count=excluded.count,
			    source_head_seq=excluded.source_head_seq,
			    checkpoint_seq=excluded.checkpoint_seq,
			    checkpointed_at=excluded.checkpointed_at`,
			counterKindPollOptionVotes, row.OptionID, row.PollID, row.Count, payload.SourceHeadSeq, checkpointSeq, payload.TS,
		); err != nil {
			return fmt.Errorf("record poll vote counter checkpoint %s/%s: %w", row.PollID, row.OptionID, err)
		}
	}
	return nil
}
