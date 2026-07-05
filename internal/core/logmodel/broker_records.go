package logmodel

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	BrokerCommandRecordVersion = 1
	BrokerEventRecordVersion   = 1
)

// BrokerCommandRecord is the broker-native representation of one gateway
// command. Offset is logical Budgie command-log state for one partition; broker
// stream offsets remain implementation details.
type BrokerCommandRecord struct {
	Version       int               `json:"v"`
	ActorID       string            `json:"actorId,omitempty"`
	CID           string            `json:"cid,omitempty"`
	Command       proto.CommandName `json:"command"`
	Payload       json.RawMessage   `json:"payload"`
	EnqueuedAt    int64             `json:"enqueuedAt"`
	PartitionKind string            `json:"partitionKind"`
	PartitionKey  string            `json:"partitionKey"`
	Offset        int64             `json:"offset"`
}

func EncodeBrokerCommandRecord(record BrokerCommandRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = BrokerCommandRecordVersion
	}
	record, err := normalizeBrokerCommandRecord(record)
	if err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodeBrokerCommandRecord(data []byte) (BrokerCommandRecord, error) {
	var record BrokerCommandRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return BrokerCommandRecord{}, err
	}
	return normalizeBrokerCommandRecord(record)
}

func normalizeBrokerCommandRecord(record BrokerCommandRecord) (BrokerCommandRecord, error) {
	if record.Version != BrokerCommandRecordVersion {
		return BrokerCommandRecord{}, fmt.Errorf("broker command record: unsupported version %d", record.Version)
	}
	if record.Command == "" {
		return BrokerCommandRecord{}, fmt.Errorf("broker command record: missing command")
	}
	if len(record.Payload) == 0 || !json.Valid(record.Payload) {
		return BrokerCommandRecord{}, fmt.Errorf("broker command record: invalid payload")
	}
	partition := Partition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.Offset <= 0 {
		return BrokerCommandRecord{}, fmt.Errorf("broker command record: missing offset")
	}
	if record.EnqueuedAt <= 0 {
		return BrokerCommandRecord{}, fmt.Errorf("broker command record: missing enqueue time")
	}
	return record, nil
}

func SameBrokerCommandRecordIdentity(existing, requested BrokerCommandRecord) bool {
	return existing.ActorID == requested.ActorID &&
		existing.CID == requested.CID &&
		existing.Command == requested.Command &&
		existing.EnqueuedAt == requested.EnqueuedAt &&
		existing.PartitionKind == requested.PartitionKind &&
		existing.PartitionKey == requested.PartitionKey &&
		string(existing.Payload) == string(requested.Payload)
}

// BrokerEventRecord is the broker-native representation of one durable event.
// PartitionOffset is logical Budgie state, not necessarily the broker's stream
// sequence. This lets shadow/parity compare against SQL partition offsets while
// each broker remains free to expose its own physical offsets.
type BrokerEventRecord struct {
	Version          int             `json:"v"`
	ID               string          `json:"id,omitempty"`
	Kind             proto.EventKind `json:"event"`
	CompatibilitySeq int64           `json:"seq,omitempty"`
	Scopes           []string        `json:"scopes,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	TS               int64           `json:"ts"`
	PartitionKind    string          `json:"partitionKind"`
	PartitionKey     string          `json:"partitionKey"`
	PartitionOffset  int64           `json:"partitionOffset"`
}

func EncodeBrokerEventRecord(record BrokerEventRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = BrokerEventRecordVersion
	}
	record, err := normalizeBrokerEventRecord(record)
	if err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodeBrokerEventRecord(data []byte) (BrokerEventRecord, error) {
	var record BrokerEventRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return BrokerEventRecord{}, err
	}
	return normalizeBrokerEventRecord(record)
}

func normalizeBrokerEventRecord(record BrokerEventRecord) (BrokerEventRecord, error) {
	if record.Version != BrokerEventRecordVersion {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: unsupported version %d", record.Version)
	}
	if record.Kind == "" {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: missing event kind")
	}
	partition := Partition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.PartitionOffset <= 0 {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: missing partition offset")
	}
	if len(record.Payload) == 0 {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: missing payload")
	}
	return record, nil
}

func SameBrokerEventRecordIdentity(existing, requested BrokerEventRecord) bool {
	if existing.ID != requested.ID ||
		existing.Kind != requested.Kind ||
		existing.CompatibilitySeq != requested.CompatibilitySeq ||
		existing.TS != requested.TS ||
		existing.PartitionKind != requested.PartitionKind ||
		existing.PartitionKey != requested.PartitionKey ||
		string(existing.Payload) != string(requested.Payload) {
		return false
	}
	if len(existing.Scopes) != len(requested.Scopes) {
		return false
	}
	for i := range existing.Scopes {
		if existing.Scopes[i] != requested.Scopes[i] {
			return false
		}
	}
	return true
}

// NormalizeBrokerEventTransactionRecords prepares broker event records for a
// transaction append before the broker assigns PartitionOffset.
func NormalizeBrokerEventTransactionRecords(records []BrokerEventRecord, duplicateScope string) ([]BrokerEventRecord, error) {
	if duplicateScope == "" {
		duplicateScope = "one transaction"
	}
	normalized := make([]BrokerEventRecord, 0, len(records))
	byID := map[string]BrokerEventRecord{}
	for _, record := range records {
		record.ID = strings.TrimSpace(record.ID)
		if record.ID == "" {
			return nil, fmt.Errorf("event id is required")
		}
		if record.Kind == "" {
			return nil, fmt.Errorf("event kind is required")
		}
		if len(record.Payload) == 0 || !json.Valid(record.Payload) {
			return nil, fmt.Errorf("event payload is not valid JSON")
		}
		partition := Partition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		record.PartitionOffset = 0
		if record.TS <= 0 {
			return nil, fmt.Errorf("event timestamp is required")
		}
		if existing, ok := byID[record.ID]; ok {
			if !SameBrokerEventRecordIdentity(existing, record) {
				return nil, fmt.Errorf("duplicate event id %q has different content", record.ID)
			}
			return nil, fmt.Errorf("duplicate event id %q in %s", record.ID, duplicateScope)
		}
		byID[record.ID] = record
		normalized = append(normalized, record)
	}
	return normalized, nil
}
