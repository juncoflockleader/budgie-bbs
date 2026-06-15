package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// currentNodeID identifies this process. Non-empty only in Postgres mode;
// used to tag pg_notify payloads so other nodes can skip self-originated events.
var currentNodeID string
var currentCrossNodeViaBus bool

func setNodeID(id string)             { currentNodeID = id }
func setCrossNodeViaBus(enabled bool) { currentCrossNodeViaBus = enabled }

// appendEvent writes a new event row and returns its assigned seq.
// Must be called within a command transaction.
// In Postgres mode it also issues a pg_notify so other nodes are woken up.
func appendEvent(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	ts := nowMS()
	partition := eventPartitionFor(kind, scopes)
	if err := acquireScalarAppendGate(tx); err != nil {
		return 0, err
	}
	partitionOffset, err := nextPartitionOffset(tx, partition)
	if err != nil {
		return 0, err
	}

	// The two storage backends use different events-table shapes. SQLite
	// denormalizes scopes/ts into the row; Postgres normalizes scopes into
	// event_scopes and stores the timestamp as created_at. Either way the
	// per-scope rows below are the authoritative scope index.
	var seq int64
	if currentSQLFlavor == postgresFlavor {
		seq, err = execReturningSeq(tx,
			`INSERT INTO events (id, kind, payload, created_at, partition_kind, partition_key, partition_offset)
			 VALUES (?,?,CAST(? AS JSONB),?,?,?,?)`,
			id, string(kind), string(raw), ts, partition.Kind, partition.Key, partitionOffset)
	} else {
		seq, err = execReturningSeq(tx,
			`INSERT INTO events (id, kind, scopes, payload, ts, partition_kind, partition_key, partition_offset)
			 VALUES (?,?,?,?,?,?,?,?)`,
			id, string(kind), strings.Join(scopes, ","), string(raw), ts, partition.Kind, partition.Key, partitionOffset)
	}
	if err != nil {
		return 0, err
	}
	for _, scope := range scopes {
		if _, err := qExec(tx,
			`INSERT INTO event_scopes (seq, scope) VALUES (?,?)
			 ON CONFLICT (seq, scope) DO NOTHING`,
			seq, scope,
		); err != nil {
			return 0, err
		}
	}

	// In Postgres mode, notify sibling nodes about this new event.
	// The NOTIFY is inside the same transaction so it's only delivered on commit.
	if currentSQLFlavor == postgresFlavor && currentNodeID != "" && !currentCrossNodeViaBus {
		notifyPayload := fmt.Sprintf(`{"seq":%d,"event":%q,"node_id":%q,"scopes":%q}`,
			seq, string(kind), currentNodeID, strings.Join(scopes, ","))
		if _, err := tx.Exec(`SELECT pg_notify($1, $2)`, pgNotifyChannel, notifyPayload); err != nil {
			// Non-fatal: LISTEN/NOTIFY is best-effort; W4 gap detection handles misses.
			slog.Warn("appendEvent: pg_notify failed", "seq", seq, "err", err)
		}
	}

	return seq, nil
}

func acquireScalarAppendGate(tx *sql.Tx) error {
	if currentSQLFlavor != postgresFlavor {
		return nil
	}
	start := time.Now()
	_, err := qExec(tx, `SELECT pg_advisory_xact_lock(?)`, pgScalarSeqAppendLockKey)
	metrics.ScalarAppendGateWait.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	return err
}

func nextPartitionOffset(tx *sql.Tx, partition eventPartition) (int64, error) {
	if partition.Kind == "" || partition.Key == "" {
		partition = defaultPartition()
	}
	if currentSQLFlavor == postgresFlavor {
		var offset int64
		err := qQueryRow(tx,
			`INSERT INTO event_partition_offsets (partition_kind, partition_key, last_offset)
			 VALUES (?,?,1)
			 ON CONFLICT (partition_kind, partition_key) DO UPDATE
			       SET last_offset=event_partition_offsets.last_offset+1
			 RETURNING last_offset`,
			partition.Kind, partition.Key,
		).Scan(&offset)
		if err != nil {
			return 0, err
		}
		return offset, nil
	}
	if _, err := qExec(tx,
		`INSERT OR IGNORE INTO event_partition_offsets (partition_kind, partition_key, last_offset)
		 VALUES (?,?,0)`,
		partition.Kind, partition.Key,
	); err != nil {
		return 0, err
	}
	if _, err := qExec(tx,
		`UPDATE event_partition_offsets
		    SET last_offset=last_offset+1
		  WHERE partition_kind=? AND partition_key=?`,
		partition.Kind, partition.Key,
	); err != nil {
		return 0, err
	}
	var offset int64
	if err := qQueryRow(tx,
		`SELECT last_offset
		   FROM event_partition_offsets
		  WHERE partition_kind=? AND partition_key=?`,
		partition.Kind, partition.Key,
	).Scan(&offset); err != nil {
		return 0, err
	}
	return offset, nil
}

// pgNotifyEphemeralFn emits a pg_notify for an ephemeral (non-durable) event.
// Used so sibling nodes can fetch the record by ID and re-publish locally.
// No-op when not in Postgres mode or when nodeID is unset.
func pgNotifyEphemeralFn(db *sql.DB, event, eid, scopes string) {
	if currentSQLFlavor != postgresFlavor || currentNodeID == "" || currentCrossNodeViaBus {
		return
	}
	payload := fmt.Sprintf(`{"seq":0,"event":%q,"node_id":%q,"scopes":%q,"eid":%q}`,
		event, currentNodeID, scopes, eid)
	if _, err := db.Exec(`SELECT pg_notify($1, $2)`, pgNotifyChannel, payload); err != nil {
		slog.Warn("pgNotifyEphemeral: failed", "event", event, "eid", eid, "err", err)
	}
}

// headSeq returns the highest seq currently in the events table.
func headSeq(db *sql.DB) (int64, error) {
	var head sql.NullInt64
	if err := qQueryRow(db, `SELECT MAX(seq) FROM events`).Scan(&head); err != nil {
		return 0, err
	}
	return head.Int64, nil
}

// replayEvents returns events with seq > after, optionally filtered to the
// given scopes (pass nil for all events).
func replayEvents(db *sql.DB, after int64, filterScopes []string, limit int) ([]*proto.Event, error) {
	query := `SELECT id, seq, kind, scopes, payload, ts, partition_kind, partition_key, partition_offset FROM events WHERE seq > ? ORDER BY seq`
	if currentSQLFlavor == postgresFlavor {
		// Postgres normalizes scopes into event_scopes and names the timestamp
		// created_at; reassemble the comma-separated scope string and alias the
		// timestamp so the scan below is identical to the SQLite path.
		query = `SELECT e.id, e.seq, e.kind,
		                COALESCE((SELECT string_agg(scope, ',') FROM event_scopes es WHERE es.seq = e.seq), '') AS scopes,
		                e.payload, e.created_at, e.partition_kind, e.partition_key, e.partition_offset
		         FROM events e WHERE e.seq > ? ORDER BY e.seq`
	}
	rows, err := qQuery(db, query, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEventRows(rows, filterScopes, limit)
}

func replayPartitionEvents(db *sql.DB, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	return replayPartitionEventsFiltered(db, partitionKind, partitionKey, afterOffset, nil, limit)
}

func replayPartitionEventsFiltered(db *sql.DB, partitionKind, partitionKey string, afterOffset int64, filterScopes []string, limit int) ([]*proto.Event, error) {
	if partitionKind == "" || partitionKey == "" {
		return nil, nil
	}
	query := `SELECT id, seq, kind, scopes, payload, ts, partition_kind, partition_key, partition_offset
	          FROM events
	         WHERE partition_kind=? AND partition_key=? AND partition_offset > ?
	         ORDER BY partition_offset, seq`
	if currentSQLFlavor == postgresFlavor {
		query = `SELECT e.id, e.seq, e.kind,
		                COALESCE((SELECT string_agg(scope, ',') FROM event_scopes es WHERE es.seq = e.seq), '') AS scopes,
		                e.payload, e.created_at, e.partition_kind, e.partition_key, e.partition_offset
		           FROM events e
		          WHERE e.partition_kind=? AND e.partition_key=? AND e.partition_offset > ?
		          ORDER BY e.partition_offset, e.seq`
	}
	rows, err := qQuery(db, query, partitionKind, partitionKey, afterOffset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEventRows(rows, filterScopes, limit)
}

func scanEventRows(rows *sql.Rows, filterScopes []string, limit int) ([]*proto.Event, error) {
	var events []*proto.Event
	for rows.Next() {
		var (
			id              string
			seq             int64
			kind            string
			scopeStr        string
			raw             string
			ts              int64
			partitionKind   string
			partitionKey    string
			partitionOffset int64
		)
		if err := rows.Scan(&id, &seq, &kind, &scopeStr, &raw, &ts, &partitionKind, &partitionKey, &partitionOffset); err != nil {
			return nil, err
		}
		evtScopes := strings.Split(scopeStr, ",")
		if filterScopes != nil && !scopesOverlap(evtScopes, filterScopes) {
			continue
		}
		p, err := unmarshalPayload(proto.EventKind(kind), []byte(raw))
		if err != nil {
			return nil, err
		}
		events = append(events, &proto.Event{
			ID:              id,
			Kind:            proto.EventKind(kind),
			Seq:             seq,
			Payload:         p,
			TS:              ts,
			PartitionKind:   partitionKind,
			PartitionKey:    partitionKey,
			PartitionOffset: partitionOffset,
			Scopes:          evtScopes,
		})
		if limit > 0 && len(events) >= limit {
			break
		}
	}
	return events, rows.Err()
}

func scopesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func unmarshalPayload(kind proto.EventKind, raw []byte) (any, error) {
	var dst any
	switch kind {
	case proto.EvtThreadNew:
		dst = new(proto.ThreadNewPayload)
	case proto.EvtPostAppended:
		dst = new(proto.PostAppendedPayload)
	case proto.EvtPostAttachmentAdded:
		dst = new(proto.PostAttachmentAddedPayload)
	case proto.EvtPostEdited:
		dst = new(proto.PostEditedPayload)
	case proto.EvtPostFlagsSet:
		dst = new(proto.PostFlagsSetPayload)
	case proto.EvtPostRedacted:
		dst = new(proto.PostRedactedPayload)
	case proto.EvtPostRestored:
		dst = new(proto.PostRestoredPayload)
	case proto.EvtPostDeletionCleared:
		dst = new(proto.PostDeletionClearedPayload)
	case proto.EvtPostPurged:
		dst = new(proto.PostPurgedPayload)
	case proto.EvtPostReacted:
		dst = new(proto.PostReactedPayload)
	case proto.EvtPostUnreacted:
		dst = new(proto.PostUnreactedPayload)
	case proto.EvtPollVoted:
		dst = new(proto.PollVotedPayload)
	case proto.EvtMentioned:
		dst = new(proto.MentionedPayload)
	case proto.EvtNotificationCreated:
		dst = new(proto.NotificationCreatedPayload)
	case proto.EvtTrustLevelChanged:
		dst = new(proto.TrustLevelChangedPayload)
	case proto.EvtPostFlagged:
		dst = new(proto.PostFlaggedPayload)
	case proto.EvtReviewResolved:
		dst = new(proto.ReviewResolvedPayload)
	case proto.EvtThreadTitleSet:
		dst = new(proto.ThreadTitleSetPayload)
	case proto.EvtThreadLocked:
		dst = new(proto.ThreadLockedPayload)
	case proto.EvtThreadMoved:
		dst = new(proto.ThreadMovedPayload)
	case proto.EvtUserSanctioned:
		dst = new(proto.UserSanctionedPayload)
	case proto.EvtUserSanctionCleared:
		dst = new(proto.UserSanctionClearedPayload)
	case proto.EvtContentFilterSet:
		dst = new(proto.ContentFilterSetPayload)
	case proto.EvtBoardAutomodRuleSet:
		dst = new(proto.BoardAutomodRuleSetPayload)
	case proto.EvtBoardAutomodRuleDeleted:
		dst = new(proto.BoardAutomodRuleDeletedPayload)
	case proto.EvtBoardAutomodTriggered:
		dst = new(proto.BoardAutomodTriggeredPayload)
	case proto.EvtRoleGranted:
		dst = new(proto.RoleGrantedPayload)
	case proto.EvtRoleRevoked:
		dst = new(proto.RoleRevokedPayload)
	case proto.EvtBoardCreated:
		dst = new(proto.BoardCreatedPayload)
	case proto.EvtBoardSettingsSet:
		dst = new(proto.BoardSettingsSetPayload)
	case proto.EvtBoardMemberRequirementsSet:
		dst = new(proto.BoardMemberRequirementsSetPayload)
	case proto.EvtBoardModeratorSet:
		dst = new(proto.BoardModeratorSetPayload)
	case proto.EvtBoardMemberSet:
		dst = new(proto.BoardMemberSetPayload)
	case proto.EvtBoardMemberApplicationSubmitted:
		dst = new(proto.BoardMemberApplicationSubmittedPayload)
	case proto.EvtBoardMemberApplicationReviewed:
		dst = new(proto.BoardMemberApplicationReviewedPayload)
	case proto.EvtBoardRecommendedSet:
		dst = new(proto.BoardRecommendedSetPayload)
	case proto.EvtBoardFavoriteSet:
		dst = new(proto.BoardFavoriteSetPayload)
	case proto.EvtFavoriteFolderCreated:
		dst = new(proto.FavoriteFolderCreatedPayload)
	case proto.EvtFavoriteFolderUpdated:
		dst = new(proto.FavoriteFolderUpdatedPayload)
	case proto.EvtFavoriteFolderDeleted:
		dst = new(proto.FavoriteFolderDeletedPayload)
	case proto.EvtFavoriteTreeImported:
		dst = new(proto.FavoriteTreeImportedPayload)
	case proto.EvtBoardZapSet:
		dst = new(proto.BoardZapSetPayload)
	case proto.EvtMailSent:
		dst = new(proto.MailSentPayload)
	case proto.EvtMailAttachmentAdded:
		dst = new(proto.MailAttachmentAddedPayload)
	case proto.EvtMailGroupSet:
		dst = new(proto.MailGroupSetPayload)
	case proto.EvtMailGroupDeleted:
		dst = new(proto.MailGroupDeletedPayload)
	case proto.EvtMailCopyUpdated:
		dst = new(proto.MailCopyUpdatedPayload)
	case proto.EvtDirectMessageSent:
		dst = new(proto.DirectMessageSentPayload)
	case proto.EvtDirectMessageRead:
		dst = new(proto.DirectMessageReadPayload)
	case proto.EvtDirectMessageDeleted:
		dst = new(proto.DirectMessageDeletedPayload)
	case proto.EvtDirectMessageSettingsSet:
		dst = new(proto.DirectMessageSettingsSetPayload)
	case proto.EvtUserRelationshipSet:
		dst = new(proto.UserRelationshipSetPayload)
	case proto.EvtUserBlessed:
		dst = new(proto.UserBlessedPayload)
	case proto.EvtDigestEntryUpserted:
		dst = new(proto.DigestEntryUpsertedPayload)
	case proto.EvtDigestEntryUpdated:
		dst = new(proto.DigestEntryUpdatedPayload)
	case proto.EvtDigestEntryBodySet:
		dst = new(proto.DigestEntryBodySetPayload)
	case proto.EvtDigestEntryRemoved:
		dst = new(proto.DigestEntryRemovedPayload)
	case proto.EvtDigestDirectorySet:
		dst = new(proto.DigestDirectorySetPayload)
	case proto.EvtDigestPathMoved:
		dst = new(proto.DigestPathMovedPayload)
	case proto.EvtDigestPathCopied:
		dst = new(proto.DigestPathCopiedPayload)
	case proto.EvtDigestPathDeleted:
		dst = new(proto.DigestPathDeletedPayload)
	case proto.EvtCommunityStatsSnapshotRecorded:
		dst = new(proto.CommunityStatsSnapshotRecordedPayload)
	case proto.EvtCounterCheckpointed:
		dst = new(proto.CounterCheckpointPayload)
	case proto.EvtChatLine:
		dst = new(proto.ChatLinePayload)
	case proto.EvtPresenceUpdate:
		dst = new(proto.PresenceUpdatePayload)
	case proto.EvtUserJoined:
		dst = new(proto.UserJoinedPayload)
	case proto.EvtUserLeft:
		dst = new(proto.UserLeftPayload)
	default:
		return json.RawMessage(raw), nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return nil, err
	}
	return dst, nil
}
