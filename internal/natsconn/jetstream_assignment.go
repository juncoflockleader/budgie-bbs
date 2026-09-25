package natsconn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	nats "github.com/nats-io/nats.go"
)

const (
	defaultJetStreamCommandAssignmentBucket = "BUDGIE_COMMAND_ASSIGNMENTS"
	jetStreamCommandAssignmentRecordVersion = 1
	jetStreamCommandAssignmentCASRetries    = 8
)

type JetStreamCommandPartitionAssignerOptions struct {
	Bucket    string
	Group     string
	Members   []string
	Overrides map[core.LogPartition]string
	Replicas  int
	Wait      time.Duration
	ReadOnly  bool
}

type JetStreamCommandPartitionAssigner struct {
	kv    commandAssignmentKV
	key   string
	group string
}

type commandAssignmentKV interface {
	Get(key string) (commandAssignmentKVEntry, error)
	Create(key string, value []byte) (uint64, error)
	Update(key string, value []byte, revision uint64) (uint64, error)
}

type commandAssignmentKVEntry interface {
	Value() []byte
	Revision() uint64
}

type natsCommandAssignmentKV struct {
	kv nats.KeyValue
}

func (k natsCommandAssignmentKV) Get(key string) (commandAssignmentKVEntry, error) {
	return k.kv.Get(key)
}

func (k natsCommandAssignmentKV) Create(key string, value []byte) (uint64, error) {
	return k.kv.Create(key, value)
}

func (k natsCommandAssignmentKV) Update(key string, value []byte, revision uint64) (uint64, error) {
	return k.kv.Update(key, value, revision)
}

type commandAssignmentRecord struct {
	Version    int               `json:"v"`
	Group      string            `json:"group"`
	Members    []string          `json:"members"`
	Overrides  map[string]string `json:"overrides,omitempty"`
	Generation int64             `json:"generation"`
	UpdatedAt  int64             `json:"updatedAt"`
}

func NewJetStreamCommandPartitionAssigner(ctx context.Context, conn *Conn, options JetStreamCommandPartitionAssignerOptions) (*JetStreamCommandPartitionAssigner, error) {
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats command assignment: nil connection")
	}
	bucket := JetStreamName(options.Bucket, defaultJetStreamCommandAssignmentBucket)
	group := JetStreamName(options.Group, defaultJetStreamCommandLogStream)
	wait := jetStreamWait(options.Wait)
	replicas := jetStreamReplicas(options.Replicas)
	js, err := conn.nc.JetStream(nats.MaxWait(wait))
	if err != nil {
		return nil, err
	}
	kv, err := js.KeyValue(bucket)
	if errors.Is(err, nats.ErrBucketNotFound) && !options.ReadOnly {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:   bucket,
			History:  16,
			Replicas: replicas,
			Storage:  nats.FileStorage,
		})
	}
	if err != nil {
		return nil, err
	}
	assigner := newJetStreamCommandPartitionAssignerWithKV(natsCommandAssignmentKV{kv: kv}, group)
	if !options.ReadOnly && len(options.Members) > 0 {
		if _, err := assigner.SetMembers(ctx, options.Members); err != nil {
			return nil, err
		}
	}
	if !options.ReadOnly && len(options.Overrides) > 0 {
		if _, err := assigner.SetOverrides(ctx, options.Overrides); err != nil {
			return nil, err
		}
	}
	return assigner, nil
}

func newJetStreamCommandPartitionAssignerWithKV(kv commandAssignmentKV, group string) *JetStreamCommandPartitionAssigner {
	group = JetStreamName(group, defaultJetStreamCommandLogStream)
	return &JetStreamCommandPartitionAssigner{
		kv:    kv,
		key:   commandAssignmentGroupKey(group),
		group: group,
	}
}

func (a *JetStreamCommandPartitionAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition core.LogPartition) (logmodel.CommandPartitionAssignment, bool, error) {
	if err := ctx.Err(); err != nil {
		return logmodel.CommandPartitionAssignment{}, false, err
	}
	if a == nil || a.kv == nil {
		return logmodel.CommandPartitionAssignment{}, false, fmt.Errorf("nats command assignment: nil assigner")
	}
	record, _, err := a.loadRecord(ctx)
	if err != nil {
		return logmodel.CommandPartitionAssignment{}, false, err
	}
	assigner := logmodel.NewHashCommandPartitionAssignerWithOverrides(record.Members, decodeCommandAssignmentOverrides(record.Overrides), record.Generation)
	return assigner.AssignCommandPartition(ctx, ownerID, partition)
}

func (a *JetStreamCommandPartitionAssigner) SetMembers(ctx context.Context, members []string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a == nil || a.kv == nil {
		return 0, fmt.Errorf("nats command assignment: nil assigner")
	}
	members = logmodel.NormalizeCommandPartitionAssignmentMembers(members)
	if len(members) == 0 {
		return 0, fmt.Errorf("nats command assignment: empty member list")
	}
	for attempt := 0; attempt < jetStreamCommandAssignmentCASRetries; attempt++ {
		record, revision, err := a.loadRecord(ctx)
		if errors.Is(err, nats.ErrKeyNotFound) {
			record = commandAssignmentRecord{
				Version:    jetStreamCommandAssignmentRecordVersion,
				Group:      a.group,
				Members:    members,
				Generation: 1,
				UpdatedAt:  time.Now().UnixMilli(),
			}
			data, err := encodeCommandAssignmentRecord(record)
			if err != nil {
				return 0, err
			}
			if _, err := a.kv.Create(a.key, data); err != nil {
				if isJetStreamCommandAssignmentCASConflict(err) {
					continue
				}
				return 0, err
			}
			return record.Generation, nil
		}
		if err != nil {
			return 0, err
		}
		if reflect.DeepEqual(record.Members, members) {
			return record.Generation, nil
		}
		record.Members = members
		record.Overrides = encodeCommandAssignmentOverrides(logmodel.NormalizeCommandPartitionAssignmentOverrides(decodeCommandAssignmentOverrides(record.Overrides), record.Members))
		record.Generation++
		if record.Generation <= 0 {
			record.Generation = 1
		}
		record.UpdatedAt = time.Now().UnixMilli()
		data, err := encodeCommandAssignmentRecord(record)
		if err != nil {
			return 0, err
		}
		if _, err := a.kv.Update(a.key, data, revision); err != nil {
			if isJetStreamCommandAssignmentCASConflict(err) {
				continue
			}
			return 0, err
		}
		return record.Generation, nil
	}
	return 0, fmt.Errorf("nats command assignment: membership CAS failed after %d attempts", jetStreamCommandAssignmentCASRetries)
}

func (a *JetStreamCommandPartitionAssigner) SetOverrides(ctx context.Context, overrides map[core.LogPartition]string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a == nil || a.kv == nil {
		return 0, fmt.Errorf("nats command assignment: nil assigner")
	}
	for attempt := 0; attempt < jetStreamCommandAssignmentCASRetries; attempt++ {
		record, revision, err := a.loadRecord(ctx)
		if err != nil {
			return 0, err
		}
		normalized, err := validateCommandAssignmentOverrides(overrides, record.Members)
		if err != nil {
			return 0, err
		}
		if reflect.DeepEqual(decodeCommandAssignmentOverrides(record.Overrides), normalized) {
			return record.Generation, nil
		}
		record.Overrides = encodeCommandAssignmentOverrides(normalized)
		record.Generation++
		if record.Generation <= 0 {
			record.Generation = 1
		}
		record.UpdatedAt = time.Now().UnixMilli()
		data, err := encodeCommandAssignmentRecord(record)
		if err != nil {
			return 0, err
		}
		if _, err := a.kv.Update(a.key, data, revision); err != nil {
			if isJetStreamCommandAssignmentCASConflict(err) {
				continue
			}
			return 0, err
		}
		return record.Generation, nil
	}
	return 0, fmt.Errorf("nats command assignment: override CAS failed after %d attempts", jetStreamCommandAssignmentCASRetries)
}

func (a *JetStreamCommandPartitionAssigner) Members(ctx context.Context) ([]string, int64, error) {
	record, _, err := a.loadRecord(ctx)
	if err != nil {
		return nil, 0, err
	}
	return append([]string(nil), record.Members...), record.Generation, nil
}

func (a *JetStreamCommandPartitionAssigner) loadRecord(ctx context.Context) (commandAssignmentRecord, uint64, error) {
	if err := ctx.Err(); err != nil {
		return commandAssignmentRecord{}, 0, err
	}
	entry, err := a.kv.Get(a.key)
	if err != nil {
		return commandAssignmentRecord{}, 0, err
	}
	record, err := decodeCommandAssignmentRecord(entry.Value())
	if err != nil {
		return commandAssignmentRecord{}, 0, err
	}
	if record.Group != a.group {
		return commandAssignmentRecord{}, 0, fmt.Errorf("nats command assignment: group mismatch %q for %q", record.Group, a.group)
	}
	return record, entry.Revision(), nil
}

func encodeCommandAssignmentRecord(record commandAssignmentRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamCommandAssignmentRecordVersion
	}
	if record.Version != jetStreamCommandAssignmentRecordVersion {
		return nil, fmt.Errorf("nats command assignment: unsupported version %d", record.Version)
	}
	record.Group = strings.TrimSpace(record.Group)
	if record.Group == "" {
		return nil, fmt.Errorf("nats command assignment: missing group")
	}
	record.Members = logmodel.NormalizeCommandPartitionAssignmentMembers(record.Members)
	if len(record.Members) == 0 {
		return nil, fmt.Errorf("nats command assignment: missing members")
	}
	record.Overrides = encodeCommandAssignmentOverrides(logmodel.NormalizeCommandPartitionAssignmentOverrides(decodeCommandAssignmentOverrides(record.Overrides), record.Members))
	if record.Generation <= 0 {
		return nil, fmt.Errorf("nats command assignment: missing generation")
	}
	if record.UpdatedAt <= 0 {
		record.UpdatedAt = time.Now().UnixMilli()
	}
	return json.Marshal(record)
}

func decodeCommandAssignmentRecord(data []byte) (commandAssignmentRecord, error) {
	var record commandAssignmentRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return commandAssignmentRecord{}, err
	}
	if record.Version != jetStreamCommandAssignmentRecordVersion {
		return commandAssignmentRecord{}, fmt.Errorf("nats command assignment: unsupported version %d", record.Version)
	}
	record.Group = strings.TrimSpace(record.Group)
	if record.Group == "" {
		return commandAssignmentRecord{}, fmt.Errorf("nats command assignment: missing group")
	}
	record.Members = logmodel.NormalizeCommandPartitionAssignmentMembers(record.Members)
	if len(record.Members) == 0 {
		return commandAssignmentRecord{}, fmt.Errorf("nats command assignment: missing members")
	}
	overrides, err := decodeCommandAssignmentOverridesStrict(record.Overrides, record.Members)
	if err != nil {
		return commandAssignmentRecord{}, err
	}
	record.Overrides = encodeCommandAssignmentOverrides(overrides)
	if record.Generation <= 0 {
		return commandAssignmentRecord{}, fmt.Errorf("nats command assignment: missing generation")
	}
	return record, nil
}

func validateCommandAssignmentOverrides(overrides map[core.LogPartition]string, members []string) (map[core.LogPartition]string, error) {
	memberSet := map[string]bool{}
	for _, member := range logmodel.NormalizeCommandPartitionAssignmentMembers(members) {
		memberSet[member] = true
	}
	out := map[core.LogPartition]string{}
	for partition, ownerID := range overrides {
		partition = partition.Normalize()
		ownerID = strings.TrimSpace(ownerID)
		if ownerID == "" {
			return nil, fmt.Errorf("nats command assignment: missing override owner for %s/%s", partition.Kind, partition.Key)
		}
		if len(memberSet) > 0 && !memberSet[ownerID] {
			return nil, fmt.Errorf("nats command assignment: override owner %q is not a member", ownerID)
		}
		out[partition] = ownerID
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func encodeCommandAssignmentOverrides(overrides map[core.LogPartition]string) map[string]string {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[string]string, len(overrides))
	for partition, ownerID := range overrides {
		partition = partition.Normalize()
		out[commandAssignmentPartitionKey(partition)] = strings.TrimSpace(ownerID)
	}
	return out
}

func decodeCommandAssignmentOverrides(raw map[string]string) map[core.LogPartition]string {
	if len(raw) == 0 {
		return nil
	}
	out := map[core.LogPartition]string{}
	for key, ownerID := range raw {
		partition, ok := parseCommandAssignmentPartitionKey(key)
		ownerID = strings.TrimSpace(ownerID)
		if !ok || ownerID == "" {
			continue
		}
		out[partition.Normalize()] = ownerID
	}
	if len(out) == 0 {
		return nil
	}
	return logmodel.NormalizeCommandPartitionAssignmentOwners(out)
}

func decodeCommandAssignmentOverridesStrict(raw map[string]string, members []string) (map[core.LogPartition]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	memberSet := map[string]bool{}
	for _, member := range logmodel.NormalizeCommandPartitionAssignmentMembers(members) {
		memberSet[member] = true
	}
	out := map[core.LogPartition]string{}
	for key, ownerID := range raw {
		partition, ok := parseCommandAssignmentPartitionKey(key)
		if !ok {
			return nil, fmt.Errorf("nats command assignment: invalid override partition %q", key)
		}
		ownerID = strings.TrimSpace(ownerID)
		if ownerID == "" {
			return nil, fmt.Errorf("nats command assignment: missing override owner for %q", key)
		}
		if len(memberSet) > 0 && !memberSet[ownerID] {
			return nil, fmt.Errorf("nats command assignment: override owner %q is not a member", ownerID)
		}
		out[partition.Normalize()] = ownerID
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func commandAssignmentPartitionKey(partition core.LogPartition) string {
	partition = partition.Normalize()
	return partition.Kind + "/" + partition.Key
}

func parseCommandAssignmentPartitionKey(raw string) (core.LogPartition, bool) {
	kind, key, ok := strings.Cut(strings.TrimSpace(raw), "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(key) == "" {
		return core.LogPartition{}, false
	}
	return core.LogPartition{Kind: strings.TrimSpace(kind), Key: strings.TrimSpace(key)}.Normalize(), true
}

func commandAssignmentGroupKey(group string) string {
	group = JetStreamName(group, defaultJetStreamCommandLogStream)
	return "group." + base64.RawURLEncoding.EncodeToString([]byte(group))
}

func isJetStreamCommandAssignmentCASConflict(err error) bool {
	return errors.Is(err, nats.ErrKeyExists) || isWrongLastSequence(err)
}
