package core

import (
	"encoding/json"

	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	partitionGlobal = logmodel.PartitionGlobal
	partitionBoard  = logmodel.PartitionBoard
	partitionThread = logmodel.PartitionThread
	partitionPost   = logmodel.PartitionPost
	partitionPoll   = logmodel.PartitionPoll
	partitionUser   = logmodel.PartitionUser
	partitionMail   = logmodel.PartitionMail
	partitionChat   = logmodel.PartitionChat
	partitionReview = logmodel.PartitionReview
)

type eventPartition struct {
	Kind   string
	Key    string
	Offset int64
}

func defaultPartition() eventPartition {
	p := logmodel.DefaultPartition()
	return eventPartition{Kind: p.Kind, Key: p.Key}
}

func eventPartitionFor(kind proto.EventKind, scopes []string) eventPartition {
	p := logmodel.EventPartitionFor(kind, scopes)
	return eventPartition{Kind: p.Kind, Key: p.Key}
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
	p, ok := logmodel.PartitionFromScopes(scopes)
	if !ok {
		return eventPartition{}, false
	}
	return eventPartition{Kind: p.Kind, Key: p.Key}, true
}

func classifyCommandPartition(actor *projections.User, name proto.CommandName, payload json.RawMessage) (eventPartition, bool) {
	actorID := ""
	if actor != nil {
		actorID = actor.ID
	}
	p, ok := logmodel.ClassifyCommandPartition(actorID, name, payload)
	if !ok {
		return eventPartition{}, false
	}
	return eventPartition{Kind: p.Kind, Key: p.Key}, true
}
