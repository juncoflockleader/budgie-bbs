package handler

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandrules"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

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

	target, errDetail := commandrules.ResolveOtherUser(tx, actor, p.User, "user not found", "cannot create a relationship with yourself")
	if errDetail != nil {
		return Reply{Err: errDetail}
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

	target, online, errDetail := commandrules.ValidateLoginWatchMutation(tx, actor, p.User, p.Active)
	if errDetail != nil {
		return Reply{Err: errDetail}
	}
	if err := tx.Commit(); err != nil {
		return internalErr(err)
	}

	if !p.Active {
		if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, commandevents.LoginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if online {
		ts := nowMS()
		if err := currentRuntime().InsertNotification(h.db, newID("notif_"), actor.ID, "login", "", "", target.Name, ts); err != nil {
			return internalErr(err)
		}
		if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, commandevents.LoginWatchRelationshipKind, "", false); err != nil {
			return internalErr(err)
		}
		return Reply{Result: &proto.AckResult{ID: target.ID}}
	}
	if err := currentRuntime().SetUserRelationship(h.db, actor.ID, target.ID, commandevents.LoginWatchRelationshipKind, "", true); err != nil {
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

	target, errDetail := commandrules.ValidateBlessUserMutation(tx, actor, p.User)
	if errDetail != nil {
		return Reply{Err: errDetail}
	}

	ts := nowMS()
	blessingID := newID("bless_")
	scopes, payload := commandevents.UserBlessed(actor.ID, actor.Name, target.ID, target.Name, blessingID, p.Message, ts)
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
		if err := currentRuntime().SetUserRelationship(h.db, watcherID, actor.ID, commandevents.LoginWatchRelationshipKind, "", false); err != nil {
			return err
		}
	}
	return nil
}
