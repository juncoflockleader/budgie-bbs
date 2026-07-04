package handler

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const LoginWatchRelationshipKind = "login_watch"

func ResolveOtherUser(queryable sqlQueryable, actor *User, ref, missingMessage, selfMessage string) (*User, Reply) {
	if actor == nil {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "authentication required", false)}
	}
	target, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if target == nil {
		return nil, Reply{Err: errDetail(proto.ErrNotFound, missingMessage, false)}
	}
	if target.ID == actor.ID {
		return nil, Reply{Err: errDetail(proto.ErrValidationFailed, selfMessage, false)}
	}
	return target, Reply{}
}

func ResolveUserRef(queryable sqlQueryable, ref string) (*User, Reply) {
	user, err := projections.FindUserRef(queryable, ref)
	if err != nil {
		return nil, internalErr(err)
	}
	if user == nil {
		return nil, Reply{Err: errDetail(proto.ErrNotFound, "user not found", false)}
	}
	return user, Reply{}
}

func ValidateLoginWatchMutation(queryable sqlQueryable, actor *User, targetRef string, active bool) (*User, bool, Reply) {
	target, reply := ResolveOtherUser(queryable, actor, targetRef, "user not found", "cannot wait for yourself")
	if reply.Err != nil {
		return nil, false, reply
	}
	if !active {
		return target, false, Reply{}
	}

	friend, err := projections.UserRelationshipExists(queryable, actor.ID, target.ID, "friend")
	if err != nil {
		return nil, false, internalErr(err)
	}
	if !friend {
		return nil, false, Reply{Err: errDetail(proto.ErrForbidden, "friend relationship required", false)}
	}
	online, err := projections.UserRecentlyOnline(queryable, target.ID)
	if err != nil {
		return nil, false, internalErr(err)
	}
	return target, online, Reply{}
}

func (h *Handler) setUserRelationship(actor *User, p proto.SetUserRelationshipPayload) Reply {
	p, msg := proto.NormalizeSetUserRelationshipPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, reply := ResolveOtherUser(tx, actor, p.User, "user not found", "cannot create a relationship with yourself")
	if reply.Err != nil {
		return reply
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, p.Kind, p.Note, p.Active); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: target.ID}}
}

func (h *Handler) setLoginWatch(actor *User, p proto.SetLoginWatchPayload) Reply {
	p, msg := proto.NormalizeSetLoginWatchPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, online, reply := ValidateLoginWatchMutation(tx, actor, p.User, p.Active)
	if reply.Err != nil {
		return reply
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if !p.Active {
		if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, LoginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if online {
		ts := nowMS()
		if err := currentRuntime().InsertNotification(h.db, newID("notif_"), actor.ID, "login", "", "", target.Name, ts); err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, LoginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, LoginWatchRelationshipKind, "", true); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: target.ID}}
}

func (h *Handler) blessUser(actor *User, p proto.BlessUserPayload) Reply {
	p, msg := proto.NormalizeBlessUserPayload(p)
	if msg != "" {
		return Reply{Err: errDetail(proto.ErrValidationFailed, msg, false)}
	}

	tx, err := h.db.Begin()
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback()

	target, reply := ValidateBlessUserMutation(tx, actor, p.User)
	if reply.Err != nil {
		return reply
	}

	ts := nowMS()
	blessingID := newID("bless_")
	scopes, payload := UserBlessedEvent(actor, target, blessingID, p.Message, ts)
	seq, err := appendEvent(tx, newID("evt_"), proto.EvtUserBlessed, scopes, payload)
	if err != nil {
		return internalErr(err)
	}
	if err := currentRuntime().InsertBlessing(tx, &Blessing{
		ID:         blessingID,
		FromUserID: actor.ID,
		FromName:   actor.Name,
		ToUserID:   target.ID,
		ToName:     target.Name,
		Message:    p.Message,
		CreatedAt:  ts,
		Seq:        seq,
	}); err != nil {
		return internalErr(err)
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	h.publishEvent(proto.EvtUserBlessed, seq, scopes, payload, ts)
	if err := h.ensureBlessingSystemPost(actor, target, blessingID, p.Message, ts); err != nil {
		return internalErr(err)
	}
	return Reply{Result: &proto.AckResult{ID: blessingID, Seq: seq}}
}

func ValidateBlessUserMutation(queryable sqlQueryable, actor *User, targetRef string) (*User, Reply) {
	target, reply := ResolveOtherUser(queryable, actor, targetRef, "user not found", "cannot bless yourself")
	if reply.Err != nil {
		return nil, reply
	}
	ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
	if err != nil {
		return nil, internalErr(err)
	}
	if ignored {
		return nil, Reply{Err: errDetail(proto.ErrForbidden, "target user ignores you", false)}
	}
	if already, err := projections.BlessingExists(queryable, actor.ID, target.ID); err != nil {
		return nil, internalErr(err)
	} else if already {
		return nil, Reply{Err: errDetail(proto.ErrConflict, "you have already blessed this user", false)}
	}
	return target, Reply{}
}

func UserBlessedEvent(actor, target *User, blessingID, message string, ts int64) ([]string, *proto.UserBlessedPayload) {
	return []string{"user:" + actor.ID, "user:" + target.ID, "blessing:" + blessingID}, &proto.UserBlessedPayload{
		ID:         blessingID,
		FromUserID: actor.ID,
		From:       actor.Name,
		ToUserID:   target.ID,
		To:         target.Name,
		Message:    message,
		TS:         ts,
	}
}

func (h *Handler) notifyLoginWatchers(actor *User, ts int64) error {
	watcherIDs, err := currentRuntime().ListLoginWatchers(h.db, actor.ID)
	if err != nil {
		return err
	}
	for _, watcherID := range watcherIDs {
		if watcherID == actor.ID {
			continue
		}
		if err := currentRuntime().InsertNotification(h.db, newID("notif_"), watcherID, "login", "", "", actor.Name, ts); err != nil {
			return err
		}
		if err := currentRuntime().SetUserRelationship(h.db, watcherID, actor.ID, LoginWatchRelationshipKind, "", false); err != nil {
			return err
		}
	}
	return nil
}
