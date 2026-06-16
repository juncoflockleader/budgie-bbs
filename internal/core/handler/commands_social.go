package handler

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const loginWatchRelationshipKind = "login_watch"

// blessingExistsTx reports whether fromID has already blessed toID.
func blessingExistsTx(tx *sql.Tx, fromID, toID string) (bool, error) {
	var found int
	err := qQueryRow(tx,
		`SELECT 1 FROM blessings WHERE from_user_id=? AND to_user_id=? LIMIT 1`,
		fromID, toID,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (h *Handler) setUserRelationship(actor *User, p proto.SetUserRelationshipPayload) Reply {
	targetRef := strings.TrimSpace(p.User)
	if targetRef == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "user is required", false)}
	}
	kind := normalizeRelationshipKind(p.Kind)
	if kind == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, `kind must be "friend" or "ignore"`, false)}
	}
	note := strings.TrimSpace(p.Note)
	if len(note) > 160 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "note is too long", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, err := findUserRefTx(tx, targetRef)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if target.ID == actor.ID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "cannot create a relationship with yourself", false)}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if err := setUserRelationship(h.db, actor.ID, target.ID, kind, note, p.Active); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: target.ID}}
}

func (h *Handler) setLoginWatch(actor *User, p proto.SetLoginWatchPayload) Reply {
	targetRef := strings.TrimSpace(p.User)
	if targetRef == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "user is required", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, err := findUserRefTx(tx, targetRef)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if target.ID == actor.ID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "cannot wait for yourself", false)}
	}
	online := false
	if p.Active {
		friend, err := relationshipExistsTx(tx, actor.ID, target.ID, "friend")
		if err != nil {
			return internalErr(err)
		}
		if !friend {
			return Reply{Err: errDetail(proto.ErrForbidden, "friend relationship required", false)}
		}
		online, err = userRecentlyOnlineTx(tx, target.ID)
		if err != nil {
			return internalErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if !p.Active {
		if err := setUserRelationship(h.db, actor.ID, target.ID, loginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if online {
		ts := nowMS()
		if err := insertNotification(h.db, newID("notif_"), actor.ID, "login", "", "", target.Name, ts); err != nil {
			return internalErr(err)
		}
		if err := setUserRelationship(h.db, actor.ID, target.ID, loginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if err := setUserRelationship(h.db, actor.ID, target.ID, loginWatchRelationshipKind, "", true); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: target.ID}}
}

func (h *Handler) blessUser(actor *User, p proto.BlessUserPayload) Reply {
	targetRef := strings.TrimSpace(p.User)
	if targetRef == "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "user is required", false)}
	}
	message := strings.TrimSpace(p.Message)
	if len(message) > 500 {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "blessing message must be 500 characters or less", false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, err := findUserRefTx(tx, targetRef)
	if err != nil {
		return internalErr(err)
	}
	if target == nil {
		return Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	if target.ID == actor.ID {
		return Reply{Err: errDetail(proto.ErrValidationFailed, "cannot bless yourself", false)}
	}
	ignored, err := relationshipExistsTx(tx, target.ID, actor.ID, "ignore")
	if err != nil {
		return internalErr(err)
	}
	if ignored {
		return Reply{Err: errDetail(proto.ErrForbidden, "target user ignores you", false)}
	}
	// One blessing per (blesser, target): the blessing ranking counts rows, so
	// without this a user could bless the same target repeatedly to inflate it.
	if already, err := blessingExistsTx(tx, actor.ID, target.ID); err != nil {
		return internalErr(err)
	} else if already {
		return Reply{Err: errDetail(proto.ErrConflict, "you have already blessed this user", false)}
	}

	ts := nowMS()
	blessingID := newID("bless_")
	scopes := []string{"user:" + actor.ID, "user:" + target.ID, "blessing:" + blessingID}
	payload := &proto.UserBlessedPayload{
		ID:         blessingID,
		FromUserID: actor.ID,
		From:       actor.Name,
		ToUserID:   target.ID,
		To:         target.Name,
		Message:    message,
		TS:         ts,
	}
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtUserBlessed, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := insertBlessing(tx, &Blessing{
		ID:         blessingID,
		FromUserID: actor.ID,
		FromName:   actor.Name,
		ToUserID:   target.ID,
		ToName:     target.Name,
		Message:    message,
		CreatedAt:  ts,
		Seq:        seq,
	}); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.bus.Publish(&proto.Event{Kind: proto.EvtUserBlessed, Seq: seq, Scopes: scopes, Payload: payload, TS: ts})
	if err := h.ensureBlessingSystemPost(actor, target, blessingID, message, ts); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: blessingID, Seq: seq}}
}

func (h *Handler) notifyLoginWatchers(actor *User, ts int64) error {
	watcherIDs, err := listLoginWatchers(h.db, actor.ID)
	if err != nil {
		return err
	}
	for _, watcherID := range watcherIDs {
		if watcherID == actor.ID {
			continue
		}
		if err := insertNotification(h.db, newID("notif_"), watcherID, "login", "", "", actor.Name, ts); err != nil {
			return err
		}
		if err := setUserRelationship(h.db, watcherID, actor.ID, loginWatchRelationshipKind, "", false); err != nil {
			return err
		}
	}
	return nil
}

func userRecentlyOnlineTx(tx *sql.Tx, userID string) (bool, error) {
	var lastSeen int64
	var status string
	err := qQueryRow(tx,
		`SELECT last_seen, status
		   FROM user_presence_sessions
		  WHERE user_id=?
		    AND LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')
		  ORDER BY last_seen DESC, updated_at DESC
		  LIMIT 1`,
		userID,
	).Scan(&lastSeen, &status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return lastSeen >= nowMS()-5*60*1000 && visiblePresenceStatus(status), nil
}

func hiddenPresenceStatus(status string) bool {
	return strings.EqualFold(status, "offline") || strings.EqualFold(status, "invisible")
}

func cloakedPresenceStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "cloak" || status == "cloaked"
}

func typingPresenceStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "typing")
}

func visiblePresenceStatus(status string) bool {
	return strings.TrimSpace(status) != "" && !hiddenPresenceStatus(status) && !cloakedPresenceStatus(status)
}

func normalizeRelationshipKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "friend", "friends", "follow", "following":
		return "friend"
	case "ignore", "ignored", "badlist":
		return "ignore"
	default:
		return ""
	}
}
