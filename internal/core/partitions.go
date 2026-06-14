package core

import (
	"encoding/json"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	partitionGlobal = "global"
	partitionBoard  = "board"
	partitionThread = "thread"
	partitionPost   = "post"
	partitionPoll   = "poll"
	partitionUser   = "user"
	partitionMail   = "mail"
	partitionChat   = "chat"
	partitionReview = "review"
)

type eventPartition struct {
	Kind   string
	Key    string
	Offset int64
}

func defaultPartition() eventPartition {
	return eventPartition{Kind: partitionGlobal, Key: partitionGlobal}
}

func eventPartitionFor(kind proto.EventKind, scopes []string) eventPartition {
	if p, ok := partitionFromScopes(scopes); ok {
		return p
	}
	return defaultPartition()
}

func ensureEventPartition(evt *proto.Event) {
	if evt == nil {
		return
	}
	if evt.PartitionKind == "" || evt.PartitionKey == "" {
		p := eventPartitionFor(evt.Kind, evt.Scopes)
		evt.PartitionKind = p.Kind
		evt.PartitionKey = p.Key
	}
	if evt.PartitionOffset == 0 && evt.Seq > 0 {
		evt.PartitionOffset = evt.Seq
	}
}

func partitionFromScopes(scopes []string) (eventPartition, bool) {
	prefixes := []struct {
		prefix string
		kind   string
	}{
		{prefix: "board:", kind: partitionBoard},
		{prefix: "thread:", kind: partitionThread},
		{prefix: "account:", kind: partitionUser},
		{prefix: "user:", kind: partitionUser},
		{prefix: "mail:", kind: partitionMail},
		{prefix: "direct:", kind: partitionUser},
		{prefix: "chat:", kind: partitionChat},
		{prefix: "presence:", kind: partitionUser},
	}
	for _, pref := range prefixes {
		for _, scope := range scopes {
			if key, ok := strings.CutPrefix(scope, pref.prefix); ok && key != "" {
				return eventPartition{Kind: pref.kind, Key: key}, true
			}
		}
	}
	return eventPartition{}, false
}

type commandPartitionSpec struct {
	kind     string
	fields   []string
	fallback string
	actor    bool
}

var commandPartitionSpecs = map[proto.CommandName]commandPartitionSpec{
	proto.CmdCreateThread:               fieldPartition(partitionBoard, "board"),
	proto.CmdAppendPost:                 fieldPartition(partitionThread, "thread"),
	proto.CmdRepostPost:                 fieldPartition(partitionBoard, "board"),
	proto.CmdPostBoardMail:              fieldPartition(partitionBoard, "board", "thread"),
	proto.CmdAttachPost:                 fieldPartition(partitionPost, "post"),
	proto.CmdEditPost:                   fieldPartition(partitionPost, "post"),
	proto.CmdSetPostFlag:                fieldPartition(partitionPost, "post"),
	proto.CmdRedactPost:                 fieldPartition(partitionPost, "post"),
	proto.CmdRestorePost:                fieldPartition(partitionPost, "post"),
	proto.CmdRedactPostRange:            fieldPartition(partitionBoard, "board"),
	proto.CmdRestorePostRange:           fieldPartition(partitionBoard, "board"),
	proto.CmdClearBoardJunk:             fieldPartition(partitionBoard, "board"),
	proto.CmdSetThreadTitle:             fieldPartition(partitionThread, "thread"),
	proto.CmdLockThread:                 fieldPartition(partitionThread, "thread"),
	proto.CmdMoveThread:                 fieldPartition(partitionThread, "thread", "toBoard"),
	proto.CmdSanctionUser:               fieldPartition(partitionUser, "user", "scope"),
	proto.CmdClearUserSanction:          fieldPartition(partitionUser, "user", "scope"),
	proto.CmdSetContentFilter:           fieldPartition(partitionBoard, "scope").withFallback(partitionGlobal),
	proto.CmdGrantRole:                  fieldPartition(partitionUser, "user"),
	proto.CmdRevokeRole:                 fieldPartition(partitionUser, "user"),
	proto.CmdPublishStatsSnapshot:       globalPartition(),
	proto.CmdPublishSystemNotice:        fieldPartition(partitionBoard, "board").withFallback(partitionGlobal),
	proto.CmdSendChatLine:               fieldPartition(partitionChat, "room").withFallback("lobby"),
	proto.CmdSetPresence:                actorPartition(partitionUser),
	proto.CmdCreateBoard:                fieldPartition(partitionBoard, "id"),
	proto.CmdSetBoardSettings:           fieldPartition(partitionBoard, "board"),
	proto.CmdSetBoardModerator:          fieldPartition(partitionBoard, "board"),
	proto.CmdSetBoardMember:             fieldPartition(partitionBoard, "board"),
	proto.CmdSetBoardMemberRequirements: fieldPartition(partitionBoard, "board"),
	proto.CmdSetRecommendedBoard:        fieldPartition(partitionBoard, "board"),
	proto.CmdApplyBoardMembership:       fieldPartition(partitionBoard, "board"),
	proto.CmdReviewBoardMembership:      fieldPartition(partitionReview, "application"),
	proto.CmdLeaveBoardMembership:       fieldPartition(partitionBoard, "board"),
	proto.CmdCuratePost:                 fieldPartition(partitionPost, "post"),
	proto.CmdCurateThread:               fieldPartition(partitionThread, "thread"),
	proto.CmdRemoveDigestEntry:          fieldPartition(partitionBoard, "board", "entry"),
	proto.CmdUpdateDigestEntry:          fieldPartition(partitionBoard, "board", "entry"),
	proto.CmdSetDigestEntryBody:         fieldPartition(partitionBoard, "board", "entry"),
	proto.CmdCreateDigestDirectory:      fieldPartition(partitionBoard, "board"),
	proto.CmdMoveDigestPath:             fieldPartition(partitionBoard, "board"),
	proto.CmdCopyDigestPath:             fieldPartition(partitionBoard, "board"),
	proto.CmdDeleteDigestPath:           fieldPartition(partitionBoard, "board"),
	proto.CmdSendDigestEntryMail:        fieldPartition(partitionMail, "entry"),
	proto.CmdMailPostAuthor:             fieldPartition(partitionPost, "post"),
	proto.CmdSendMail:                   actorPartition(partitionMail),
	proto.CmdForwardMail:                fieldPartition(partitionMail, "mail"),
	proto.CmdPostMailToBoard:            fieldPartition(partitionBoard, "board", "thread", "mail"),
	proto.CmdSetMailGroup:               fieldPartition(partitionMail, "group").withActorFallback(),
	proto.CmdDeleteMailGroup:            fieldPartition(partitionMail, "group"),
	proto.CmdAttachMail:                 fieldPartition(partitionMail, "mail"),
	proto.CmdUpdateMail:                 fieldPartition(partitionMail, "mail"),
	proto.CmdDeleteMail:                 fieldPartition(partitionMail, "mail"),
	proto.CmdDeleteMailRange:            fieldPartition(partitionMail, "mail"),
	proto.CmdSendDirectMessage:          fieldPartition(partitionUser, "to"),
	proto.CmdSetDirectMessageSettings:   actorPartition(partitionUser),
	proto.CmdMarkDirectMessageRead:      fieldPartition(partitionUser, "message").withActorFallback(),
	proto.CmdDeleteDirectMessage:        fieldPartition(partitionUser, "message").withActorFallback(),
	proto.CmdSetUserRelationship:        fieldPartition(partitionUser, "user"),
	proto.CmdSetLoginWatch:              fieldPartition(partitionUser, "user"),
	proto.CmdBlessUser:                  fieldPartition(partitionUser, "user"),
	proto.CmdSetBoardFavorite:           fieldPartition(partitionBoard, "board"),
	proto.CmdSetBoardZap:                fieldPartition(partitionBoard, "board"),
	proto.CmdCreateFavoriteFolder:       actorPartition(partitionUser),
	proto.CmdUpdateFavoriteFolder:       fieldPartition(partitionUser, "folder").withActorFallback(),
	proto.CmdDeleteFavoriteFolder:       fieldPartition(partitionUser, "folder").withActorFallback(),
	proto.CmdMoveBoardFavorite:          fieldPartition(partitionBoard, "board"),
	proto.CmdImportFavoriteTree:         actorPartition(partitionUser),
	proto.CmdMarkBoardRead:              fieldPartition(partitionBoard, "board"),
	proto.CmdRestoreBoardRead:           fieldPartition(partitionBoard, "board"),
	proto.CmdMarkFavoriteFolderRead:     fieldPartition(partitionUser, "folder").withActorFallback(),
	proto.CmdRestoreFavoriteFolderRead:  fieldPartition(partitionUser, "folder").withActorFallback(),
	proto.CmdMarkThreadRead:             fieldPartition(partitionThread, "thread"),
	proto.CmdRestoreThreadRead:          fieldPartition(partitionThread, "thread"),
	proto.CmdMarkPostRead:               fieldPartition(partitionPost, "post"),
	proto.CmdPurgePost:                  fieldPartition(partitionPost, "post"),
	proto.CmdReactPost:                  fieldPartition(partitionPost, "post"),
	proto.CmdUnreactPost:                fieldPartition(partitionPost, "post"),
	proto.CmdVotePoll:                   fieldPartition(partitionPoll, "poll"),
	proto.CmdPublishPollResult:          fieldPartition(partitionPoll, "poll"),
	proto.CmdSetThreadPref:              fieldPartition(partitionThread, "thread"),
	proto.CmdFlagPost:                   fieldPartition(partitionPost, "post"),
	proto.CmdResolveReview:              fieldPartition(partitionReview, "review"),
}

func fieldPartition(kind string, fields ...string) commandPartitionSpec {
	return commandPartitionSpec{kind: kind, fields: fields}
}

func actorPartition(kind string) commandPartitionSpec {
	return commandPartitionSpec{kind: kind, actor: true}
}

func globalPartition() commandPartitionSpec {
	return commandPartitionSpec{kind: partitionGlobal, fallback: partitionGlobal}
}

func (s commandPartitionSpec) withFallback(key string) commandPartitionSpec {
	s.fallback = key
	return s
}

func (s commandPartitionSpec) withActorFallback() commandPartitionSpec {
	s.actor = true
	return s
}

func classifyCommandPartition(actor *User, name proto.CommandName, payload json.RawMessage) (eventPartition, bool) {
	spec, ok := commandPartitionSpecs[name]
	if !ok {
		return eventPartition{}, false
	}
	var raw map[string]any
	_ = json.Unmarshal(payload, &raw)
	for _, field := range spec.fields {
		if key := jsonString(raw[field]); key != "" {
			return eventPartition{Kind: spec.kind, Key: key}, true
		}
	}
	if spec.actor && actor != nil && actor.ID != "" {
		return eventPartition{Kind: spec.kind, Key: actor.ID}, true
	}
	if spec.fallback != "" {
		return eventPartition{Kind: spec.kind, Key: spec.fallback}, true
	}
	return eventPartition{Kind: spec.kind, Key: partitionGlobal}, true
}

func jsonString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, item := range x {
			if s := jsonString(item); s != "" {
				return s
			}
		}
	}
	return ""
}
