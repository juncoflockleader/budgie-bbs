package handler

import (
	"encoding/json"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// --- Idempotency wrapper ---

func (h *Handler) dispatch(actor *User, name proto.CommandName, payload json.RawMessage, cid string) Reply {
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	commandHash := hashCommand(name, payload)
	if cid != "" {
		if cached, ok, conflict := checkProcessed(h.db, actorID, cid, commandHash); conflict {
			return Reply{Err: errDetail(proto.ErrConflict, "command id was already used with a different payload", false)}
		} else if ok {
			var r proto.AckResult
			_ = json.Unmarshal([]byte(cached), &r)
			return Reply{Result: &r}
		}
	}

	reply := h.route(actor, name, payload)

	if cid != "" && reply.Err == nil && reply.Result != nil {
		raw, _ := json.Marshal(reply.Result)
		// Record inside its own tiny tx; non-fatal if it fails.
		tx, err := h.db.Begin()
		if err == nil {
			_ = recordProcessed(tx, actorID, cid, commandHash, string(raw))
			_ = tx.Commit()
		}
	}
	return reply
}

func (h *Handler) route(actor *User, name proto.CommandName, payload json.RawMessage) Reply {
	switch name {
	case proto.CmdCreateThread:
		var p proto.CreateThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createThread(actor, p)

	case proto.CmdAppendPost:
		var p proto.AppendPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.appendPost(actor, p)

	case proto.CmdEditPost:
		var p proto.EditPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.editPost(actor, p)

	case proto.CmdRedactPost:
		var p proto.RedactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.redactPost(actor, p)

	case proto.CmdRestorePost:
		var p proto.RestorePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.restorePost(actor, p)

	case proto.CmdLockThread:
		var p proto.LockThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.lockThread(actor, p)

	case proto.CmdMoveThread:
		var p proto.MoveThreadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.moveThread(actor, p)

	case proto.CmdGrantRole:
		var p proto.GrantRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.grantRole(actor, p)

	case proto.CmdRevokeRole:
		var p proto.RevokeRolePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.revokeRole(actor, p)

	case proto.CmdSendChatLine:
		var p proto.SendChatLinePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sendChatLine(actor, p)

	case proto.CmdSetPresence:
		var p proto.SetPresencePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setPresence(actor, p)

	case proto.CmdSanctionUser:
		var p proto.SanctionUserPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.sanctionUser(actor, p)

	case proto.CmdCreateBoard:
		var p proto.CreateBoardPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.createBoard(actor, p)

	case proto.CmdPurgePost:
		var p proto.PurgePostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.purgePost(actor, p)

	case proto.CmdReactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.reactPost(actor, p)

	case proto.CmdUnreactPost:
		var p proto.ReactPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.unreactPost(actor, p)

	case proto.CmdVotePoll:
		var p proto.VotePollPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.votePoll(actor, p)

	case proto.CmdSetThreadPref:
		var p proto.SetThreadPrefPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.setThreadPref(actor, p)

	case proto.CmdFlagPost:
		var p proto.FlagPostPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.flagPost(actor, p)

	case proto.CmdResolveReview:
		var p proto.ResolveReviewPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return badPayload()
		}
		return h.resolveReview(actor, p)

	default:
		return Reply{Err: errDetail(proto.ErrValidationFailed, fmt.Sprintf("unknown command: %s", name), false)}
	}
}
