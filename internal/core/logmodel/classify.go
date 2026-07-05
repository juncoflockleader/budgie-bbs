package logmodel

import (
	"encoding/json"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func DefaultPartition() Partition {
	return Partition{Kind: PartitionGlobal, Key: PartitionGlobal}
}

func EventPartitionFor(kind proto.EventKind, scopes []string) Partition {
	if p, ok := PartitionFromScopes(scopes); ok {
		return p
	}
	return DefaultPartition()
}

func PartitionFromScopes(scopes []string) (Partition, bool) {
	prefixes := []struct {
		prefix string
		kind   string
	}{
		{prefix: "board:", kind: PartitionBoard},
		{prefix: "thread:", kind: PartitionThread},
		{prefix: "account:", kind: PartitionUser},
		{prefix: "user:", kind: PartitionUser},
		{prefix: "mail:", kind: PartitionMail},
		{prefix: "direct:", kind: PartitionUser},
		{prefix: "chat:", kind: PartitionChat},
		{prefix: "presence:", kind: PartitionUser},
	}
	for _, pref := range prefixes {
		for _, scope := range scopes {
			if key, ok := strings.CutPrefix(scope, pref.prefix); ok && key != "" {
				return Partition{Kind: pref.kind, Key: key}, true
			}
		}
	}
	return Partition{}, false
}

type commandPartitionSpec struct {
	kind     string
	fields   []string
	fallback string
	actor    bool
}

var commandPartitionSpecs = map[proto.CommandName]commandPartitionSpec{
	proto.CmdCreateThread:               fieldPartition(PartitionBoard, "board"),
	proto.CmdAppendPost:                 fieldPartition(PartitionThread, "thread"),
	proto.CmdRepostPost:                 fieldPartition(PartitionBoard, "board"),
	proto.CmdPostBoardMail:              fieldPartition(PartitionBoard, "board", "thread"),
	proto.CmdAttachPost:                 fieldPartition(PartitionPost, "post"),
	proto.CmdEditPost:                   fieldPartition(PartitionPost, "post"),
	proto.CmdSetPostFlag:                fieldPartition(PartitionPost, "post"),
	proto.CmdRedactPost:                 fieldPartition(PartitionPost, "post"),
	proto.CmdRestorePost:                fieldPartition(PartitionPost, "post"),
	proto.CmdRedactPostRange:            fieldPartition(PartitionBoard, "board"),
	proto.CmdRestorePostRange:           fieldPartition(PartitionBoard, "board"),
	proto.CmdClearBoardJunk:             fieldPartition(PartitionBoard, "board"),
	proto.CmdSetThreadTitle:             fieldPartition(PartitionThread, "thread"),
	proto.CmdLockThread:                 fieldPartition(PartitionThread, "thread"),
	proto.CmdMoveThread:                 fieldPartition(PartitionThread, "thread", "toBoard"),
	proto.CmdSanctionUser:               fieldPartition(PartitionUser, "user", "scope"),
	proto.CmdClearUserSanction:          fieldPartition(PartitionUser, "user", "scope"),
	proto.CmdSetContentFilter:           fieldPartition(PartitionBoard, "scope").withFallback(PartitionGlobal),
	proto.CmdSetBoardAutomodRule:        fieldPartition(PartitionBoard, "board"),
	proto.CmdDeleteBoardAutomodRule:     fieldPartition(PartitionBoard, "board"),
	proto.CmdGrantRole:                  fieldPartition(PartitionUser, "user"),
	proto.CmdRevokeRole:                 fieldPartition(PartitionUser, "user"),
	proto.CmdPublishStatsSnapshot:       globalPartition(),
	proto.CmdPublishSystemNotice:        fieldPartition(PartitionBoard, "board").withFallback(PartitionGlobal),
	proto.CmdSendChatLine:               fieldPartition(PartitionChat, "room").withFallback("lobby"),
	proto.CmdSetPresence:                actorPartition(PartitionUser),
	proto.CmdMUDCommand:                 actorPartition(PartitionUser),
	proto.CmdCreateBoard:                fieldPartition(PartitionBoard, "id"),
	proto.CmdSetBoardSettings:           fieldPartition(PartitionBoard, "board"),
	proto.CmdSetBoardModerator:          fieldPartition(PartitionBoard, "board"),
	proto.CmdSetBoardMember:             fieldPartition(PartitionBoard, "board"),
	proto.CmdSetBoardMemberRequirements: fieldPartition(PartitionBoard, "board"),
	proto.CmdSetRecommendedBoard:        fieldPartition(PartitionBoard, "board"),
	proto.CmdApplyBoardMembership:       fieldPartition(PartitionBoard, "board"),
	proto.CmdReviewBoardMembership:      fieldPartition(PartitionReview, "application"),
	proto.CmdLeaveBoardMembership:       fieldPartition(PartitionBoard, "board"),
	proto.CmdCuratePost:                 fieldPartition(PartitionPost, "post"),
	proto.CmdCurateThread:               fieldPartition(PartitionThread, "thread"),
	proto.CmdRemoveDigestEntry:          fieldPartition(PartitionBoard, "board", "entry"),
	proto.CmdUpdateDigestEntry:          fieldPartition(PartitionBoard, "board", "entry"),
	proto.CmdSetDigestEntryBody:         fieldPartition(PartitionBoard, "board", "entry"),
	proto.CmdCreateDigestDirectory:      fieldPartition(PartitionBoard, "board"),
	proto.CmdMoveDigestPath:             fieldPartition(PartitionBoard, "board"),
	proto.CmdCopyDigestPath:             fieldPartition(PartitionBoard, "board"),
	proto.CmdDeleteDigestPath:           fieldPartition(PartitionBoard, "board"),
	proto.CmdSendDigestEntryMail:        fieldPartition(PartitionMail, "entry"),
	proto.CmdMailPostAuthor:             fieldPartition(PartitionPost, "post"),
	proto.CmdSendMail:                   actorPartition(PartitionMail),
	proto.CmdForwardMail:                fieldPartition(PartitionMail, "mail"),
	proto.CmdPostMailToBoard:            fieldPartition(PartitionBoard, "board", "thread", "mail"),
	proto.CmdSetMailGroup:               fieldPartition(PartitionMail, "group").withActorFallback(),
	proto.CmdDeleteMailGroup:            fieldPartition(PartitionMail, "group"),
	proto.CmdAttachMail:                 fieldPartition(PartitionMail, "mail"),
	proto.CmdUpdateMail:                 fieldPartition(PartitionMail, "mail"),
	proto.CmdDeleteMail:                 fieldPartition(PartitionMail, "mail"),
	proto.CmdDeleteMailRange:            fieldPartition(PartitionMail, "mail"),
	proto.CmdSendDirectMessage:          fieldPartition(PartitionUser, "to"),
	proto.CmdSetDirectMessageSettings:   actorPartition(PartitionUser),
	proto.CmdMarkDirectMessageRead:      fieldPartition(PartitionUser, "message").withActorFallback(),
	proto.CmdDeleteDirectMessage:        fieldPartition(PartitionUser, "message").withActorFallback(),
	proto.CmdSetUserRelationship:        fieldPartition(PartitionUser, "user"),
	proto.CmdSetLoginWatch:              fieldPartition(PartitionUser, "user"),
	proto.CmdBlessUser:                  fieldPartition(PartitionUser, "user"),
	proto.CmdSetBoardFavorite:           fieldPartition(PartitionBoard, "board"),
	proto.CmdSetBoardZap:                fieldPartition(PartitionBoard, "board"),
	proto.CmdCreateFavoriteFolder:       actorPartition(PartitionUser),
	proto.CmdUpdateFavoriteFolder:       fieldPartition(PartitionUser, "folder").withActorFallback(),
	proto.CmdDeleteFavoriteFolder:       fieldPartition(PartitionUser, "folder").withActorFallback(),
	proto.CmdMoveBoardFavorite:          fieldPartition(PartitionBoard, "board"),
	proto.CmdImportFavoriteTree:         actorPartition(PartitionUser),
	proto.CmdMarkBoardRead:              fieldPartition(PartitionBoard, "board"),
	proto.CmdRestoreBoardRead:           fieldPartition(PartitionBoard, "board"),
	proto.CmdMarkFavoriteFolderRead:     fieldPartition(PartitionUser, "folder").withActorFallback(),
	proto.CmdRestoreFavoriteFolderRead:  fieldPartition(PartitionUser, "folder").withActorFallback(),
	proto.CmdMarkThreadRead:             fieldPartition(PartitionThread, "thread"),
	proto.CmdRestoreThreadRead:          fieldPartition(PartitionThread, "thread"),
	proto.CmdMarkPostRead:               fieldPartition(PartitionPost, "post"),
	proto.CmdPurgePost:                  fieldPartition(PartitionPost, "post"),
	proto.CmdReactPost:                  fieldPartition(PartitionPost, "post"),
	proto.CmdUnreactPost:                fieldPartition(PartitionPost, "post"),
	proto.CmdVotePoll:                   fieldPartition(PartitionPoll, "poll"),
	proto.CmdPublishPollResult:          fieldPartition(PartitionPoll, "poll"),
	proto.CmdSetThreadPref:              fieldPartition(PartitionThread, "thread"),
	proto.CmdFlagPost:                   fieldPartition(PartitionPost, "post"),
	proto.CmdResolveReview:              fieldPartition(PartitionReview, "review"),
}

func HasCommandPartitionSpec(name proto.CommandName) bool {
	_, ok := commandPartitionSpecs[name]
	return ok
}

func CommandBypassesCommandLog(name proto.CommandName) bool {
	switch name {
	case proto.CmdSendChatLine,
		proto.CmdMUDCommand,
		proto.CmdSetPresence,
		proto.CmdReactPost,
		proto.CmdUnreactPost,
		proto.CmdVotePoll,
		proto.CmdMarkBoardRead,
		proto.CmdRestoreBoardRead,
		proto.CmdMarkFavoriteFolderRead,
		proto.CmdRestoreFavoriteFolderRead,
		proto.CmdMarkThreadRead,
		proto.CmdRestoreThreadRead,
		proto.CmdMarkPostRead,
		proto.CmdSetThreadPref,
		proto.CmdSubscribe,
		proto.CmdUnsubscribe:
		return true
	default:
		return false
	}
}

func fieldPartition(kind string, fields ...string) commandPartitionSpec {
	return commandPartitionSpec{kind: kind, fields: fields}
}

func actorPartition(kind string) commandPartitionSpec {
	return commandPartitionSpec{kind: kind, actor: true}
}

func globalPartition() commandPartitionSpec {
	return commandPartitionSpec{kind: PartitionGlobal, fallback: PartitionGlobal}
}

func (s commandPartitionSpec) withFallback(key string) commandPartitionSpec {
	s.fallback = key
	return s
}

func (s commandPartitionSpec) withActorFallback() commandPartitionSpec {
	s.actor = true
	return s
}

func ClassifyCommandPartition(actorID string, name proto.CommandName, payload json.RawMessage) (Partition, bool) {
	spec, ok := commandPartitionSpecs[name]
	if !ok {
		return Partition{}, false
	}
	var raw map[string]any
	_ = json.Unmarshal(payload, &raw)
	for _, field := range spec.fields {
		if key := JSONString(raw[field]); key != "" {
			return Partition{Kind: spec.kind, Key: key}, true
		}
	}
	if spec.actor && actorID != "" {
		return Partition{Kind: spec.kind, Key: actorID}, true
	}
	if spec.fallback != "" {
		return Partition{Kind: spec.kind, Key: spec.fallback}, true
	}
	return Partition{Kind: spec.kind, Key: PartitionGlobal}, true
}

func CommandPartitionMatchesAppendPostTarget(command proto.CommandName, payload json.RawMessage, actual Partition) bool {
	if command != proto.CmdAppendPost {
		return false
	}
	actual = actual.Normalize()
	if actual.Kind != PartitionThread {
		return false
	}
	var raw map[string]any
	_ = json.Unmarshal(payload, &raw)
	threadID := JSONString(raw["thread"])
	if threadID == "" {
		return false
	}
	if actual.Key == threadID {
		return true
	}
	baseThreadID, ok := HotThreadSplitPartitionThread(actual.Key)
	return ok && baseThreadID == threadID
}

func JSONString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, item := range x {
			if s := JSONString(item); s != "" {
				return s
			}
		}
	}
	return ""
}
