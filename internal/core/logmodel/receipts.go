package logmodel

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	deterministicCommandReceiptBaseUnixMS = int64(1704067200000) // 2024-01-01T00:00:00Z.
	deterministicCommandReceiptWindowMS   = uint64(10 * 365 * 24 * 60 * 60 * 1000)
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

func EffectiveCommandLogCID(record CommandLogRecord) (string, error) {
	if record.CID != "" {
		return record.CID, nil
	}
	if record.Offset <= 0 {
		return "", fmt.Errorf("command log record missing offset")
	}
	return SyntheticCommandLogCID(record.Partition, record.Offset), nil
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

func DeterministicCommandReceiptEnqueuedAt(partition Partition, actorID, cid string, command proto.CommandName, payload []byte) int64 {
	partition = partition.Normalize()
	h := fnv.New64a()
	_, _ = h.Write([]byte(partition.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(partition.Key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(actorID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cid))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(command))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return deterministicCommandReceiptBaseUnixMS + int64(h.Sum64()%deterministicCommandReceiptWindowMS) + 1
}
