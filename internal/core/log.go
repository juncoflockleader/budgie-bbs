package core

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// appendEvent writes a new event row and returns its assigned seq.
// Must be called within a transaction from the single-writer goroutine.
func appendEvent(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	query := `INSERT INTO events (id, kind, scopes, payload, ts) VALUES (?,?,?,?,?)`
	if currentSQLFlavor == postgresFlavor {
		query = `INSERT INTO events (id, kind, scopes, payload, ts) VALUES (?,?,?,CAST(? AS JSONB),?)`
	}

	res, err := execReturningSeq(tx, query, id, string(kind), strings.Join(scopes, ","), string(raw), nowMS())
	if err != nil {
		return 0, err
	}
	seq := res
	for _, scope := range scopes {
		if _, err := qExec(tx,
			`INSERT INTO event_scopes (seq, scope) VALUES (?,?)
			 ON CONFLICT (seq, scope) DO NOTHING`,
			seq, scope,
		); err != nil {
			return 0, err
		}
	}
	return seq, nil
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
	rows, err := qQuery(db,
		`SELECT seq, kind, scopes, payload, ts FROM events WHERE seq > ? ORDER BY seq`,
		after,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*proto.Event
	for rows.Next() {
		var (
			seq      int64
			kind     string
			scopeStr string
			raw      string
			ts       int64
		)
		if err := rows.Scan(&seq, &kind, &scopeStr, &raw, &ts); err != nil {
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
			Kind:    proto.EventKind(kind),
			Seq:     seq,
			Payload: p,
			TS:      ts,
			Scopes:  evtScopes,
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
	case proto.EvtRoleGranted:
		dst = new(proto.RoleGrantedPayload)
	case proto.EvtRoleRevoked:
		dst = new(proto.RoleRevokedPayload)
	case proto.EvtBoardCreated:
		dst = new(proto.BoardCreatedPayload)
	case proto.EvtMailSent:
		dst = new(proto.MailSentPayload)
	case proto.EvtMailAttachmentAdded:
		dst = new(proto.MailAttachmentAddedPayload)
	case proto.EvtDirectMessageSent:
		dst = new(proto.DirectMessageSentPayload)
	case proto.EvtUserBlessed:
		dst = new(proto.UserBlessedPayload)
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
