package logmodel

import (
	"fmt"
	"strings"
)

type CommandReceiptKey struct {
	Partition Partition
	ActorID   string
	CID       string
}

func NewCommandReceiptKey(partition Partition, actorID, cid string) (CommandReceiptKey, bool) {
	cid = strings.TrimSpace(cid)
	if cid == "" {
		return CommandReceiptKey{}, false
	}
	return CommandReceiptKey{
		Partition: partition.Normalize(),
		ActorID:   actorID,
		CID:       cid,
	}, true
}

func SyntheticCommandLogCID(partition Partition, offset int64) string {
	partition = partition.Normalize()
	return fmt.Sprintf("budgie:command-log:%s:%s:%d", partition.Kind, partition.Key, offset)
}

func CommandReceiptMessageID(prefix string, partition Partition, actorID, cid string) string {
	key, ok := NewCommandReceiptKey(partition, actorID, cid)
	if !ok {
		return ""
	}
	return prefix +
		".id." + EncodeSubjectToken(key.Partition.Kind) +
		"." + EncodeSubjectToken(key.Partition.Key) +
		"." + EncodeSubjectToken(key.ActorID) +
		"." + EncodeSubjectToken(key.CID)
}
