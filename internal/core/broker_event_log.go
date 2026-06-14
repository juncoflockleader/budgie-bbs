package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const (
	brokerEventRecordVersion = 1
	brokerEventSubjectPrefix = "budgie.eventlog"
)

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

// BrokerEventLogMessage is a physical broker message plus its logical event
// offset. StreamSeq is optional broker metadata used only for scalar diagnostics.
type BrokerEventLogMessage struct {
	Partition LogPartition
	Offset    int64
	StreamSeq int64
	Data      []byte
}

// BrokerEventLogClient is the minimal durable broker boundary needed to shadow
// SQL events before the broker becomes the authoritative event log.
type BrokerEventLogClient interface {
	AppendEvent(ctx context.Context, partition LogPartition, record BrokerEventRecord) (BrokerEventLogMessage, error)
	FetchEvents(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]BrokerEventLogMessage, error)
	Head(ctx context.Context) (int64, error)
}

// BrokerEventStore adapts a partitioned broker event log to EventStore.
type BrokerEventStore struct {
	client BrokerEventLogClient
}

func NewBrokerEventStore(client BrokerEventLogClient) *BrokerEventStore {
	return &BrokerEventStore{client: client}
}

func (s *BrokerEventStore) RequiresEventStoreProjectionWatermarkSeed() bool {
	seeder, ok := s.client.(interface {
		RequiresEventStoreProjectionWatermarkSeed() bool
	})
	return ok && seeder.RequiresEventStoreProjectionWatermarkSeed()
}

func (s *BrokerEventStore) Append(ctx context.Context, event EventAppend) (*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("broker event store: nil client")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return nil, err
	}
	if _, err := unmarshalPayload(event.Kind, payload); err != nil {
		return nil, err
	}
	id := event.ID
	if id == "" {
		id = newID("evt_")
	} else if event.TS <= 0 {
		return nil, fmt.Errorf("broker event store: event timestamp is required when event id is set")
	}
	partition := logPartitionFromEventPartition(eventPartitionFor(event.Kind, event.Scopes))
	msg, err := s.client.AppendEvent(ctx, partition, BrokerEventRecord{
		Version:          brokerEventRecordVersion,
		ID:               id,
		Kind:             event.Kind,
		CompatibilitySeq: event.CompatibilitySeq,
		Scopes:           append([]string(nil), event.Scopes...),
		Payload:          append([]byte(nil), payload...),
		TS:               eventAppendTS(event.TS),
		PartitionKind:    partition.Kind,
		PartitionKey:     partition.Key,
		PartitionOffset:  event.PartitionOffset,
	})
	if err != nil {
		return nil, err
	}
	return DecodeBrokerEventMessage(msg)
}

func (s *BrokerEventStore) Head(ctx context.Context) (int64, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("broker event store: nil client")
	}
	return s.client.Head(ctx)
}

func (s *BrokerEventStore) Replay(ctx context.Context, after int64, scopes []string, limit int) ([]*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("broker event store: nil client")
	}
	lister, ok := s.client.(interface {
		ListEventPartitions(context.Context, int) ([]LogPartition, error)
	})
	if !ok {
		return nil, fmt.Errorf("broker event store: scalar replay requires partition listing")
	}
	partitions, err := lister.ListEventPartitions(ctx, 0)
	if err != nil {
		return nil, err
	}
	events := make([]*proto.Event, 0)
	for _, partition := range partitions {
		partition = partition.Normalize()
		afterOffset := int64(0)
		for {
			messages, err := s.client.FetchEvents(ctx, partition, afterOffset, 100)
			if err != nil {
				return nil, err
			}
			if len(messages) == 0 {
				break
			}
			for _, msg := range messages {
				evt, err := DecodeBrokerEventMessage(msg)
				if err != nil {
					return nil, err
				}
				if evt.PartitionOffset > afterOffset {
					afterOffset = evt.PartitionOffset
				}
				if evt.Seq <= after {
					continue
				}
				if scopes != nil && !scopesOverlap(evt.Scopes, scopes) {
					continue
				}
				events = append(events, evt)
			}
			if len(messages) < 100 {
				break
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Seq == events[j].Seq {
			if events[i].PartitionKind == events[j].PartitionKind {
				if events[i].PartitionKey == events[j].PartitionKey {
					return events[i].PartitionOffset < events[j].PartitionOffset
				}
				return events[i].PartitionKey < events[j].PartitionKey
			}
			return events[i].PartitionKind < events[j].PartitionKind
		}
		return events[i].Seq < events[j].Seq
	})
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (s *BrokerEventStore) ReplayPartition(ctx context.Context, partitionKind, partitionKey string, afterOffset int64, limit int) ([]*proto.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("broker event store: nil client")
	}
	partition := LogPartition{Kind: partitionKind, Key: partitionKey}.Normalize()
	messages, err := s.client.FetchEvents(ctx, partition, afterOffset, limit)
	if err != nil {
		return nil, err
	}
	events := make([]*proto.Event, 0, len(messages))
	for _, msg := range messages {
		evt, err := DecodeBrokerEventMessage(msg)
		if err != nil {
			return nil, err
		}
		if evt.PartitionKind != partition.Kind || evt.PartitionKey != partition.Key {
			return nil, fmt.Errorf("broker event store: replay returned wrong partition %s/%s for %s/%s",
				evt.PartitionKind, evt.PartitionKey, partition.Kind, partition.Key)
		}
		if evt.PartitionOffset <= afterOffset {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}

func (s *BrokerEventStore) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	lister, ok := s.client.(interface {
		ListEventPartitions(context.Context, int) ([]LogPartition, error)
	})
	if !ok {
		return nil, fmt.Errorf("broker event store: partition listing is not supported")
	}
	return lister.ListEventPartitions(ctx, limit)
}

func (s *BrokerEventStore) ListEventPartitionOffsets(ctx context.Context, limit int) ([]EventPartitionOffset, error) {
	lister, ok := s.client.(EventPartitionOffsetLister)
	if !ok {
		return nil, fmt.Errorf("broker event store: partition offset listing is not supported")
	}
	return lister.ListEventPartitionOffsets(ctx, limit)
}

func (s *BrokerEventStore) SeedEventPartitionOffset(ctx context.Context, partition LogPartition, offset int64) error {
	seeder, ok := s.client.(interface {
		SeedEventPartitionOffset(context.Context, LogPartition, int64) error
	})
	if !ok {
		return fmt.Errorf("broker event store: partition offset seeding is not supported")
	}
	return seeder.SeedEventPartitionOffset(ctx, partition, offset)
}

func EncodeBrokerEventRecord(record BrokerEventRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = brokerEventRecordVersion
	}
	if record.Version != brokerEventRecordVersion {
		return nil, fmt.Errorf("broker event record: unsupported version %d", record.Version)
	}
	if record.Kind == "" {
		return nil, fmt.Errorf("broker event record: missing event kind")
	}
	partition := LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.PartitionOffset <= 0 {
		return nil, fmt.Errorf("broker event record: missing partition offset")
	}
	if len(record.Payload) == 0 {
		return nil, fmt.Errorf("broker event record: missing payload")
	}
	return json.Marshal(record)
}

func DecodeBrokerEventRecord(data []byte) (BrokerEventRecord, error) {
	var record BrokerEventRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return BrokerEventRecord{}, err
	}
	if record.Version != brokerEventRecordVersion {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: unsupported version %d", record.Version)
	}
	if record.Kind == "" {
		return BrokerEventRecord{}, fmt.Errorf("broker event record: missing event kind")
	}
	partition := LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
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

func DecodeBrokerEventMessage(msg BrokerEventLogMessage) (*proto.Event, error) {
	record, err := DecodeBrokerEventRecord(msg.Data)
	if err != nil {
		return nil, err
	}
	partition := LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	if msg.Partition != (LogPartition{}) && msg.Partition.Normalize() != partition {
		return nil, fmt.Errorf("broker event message: partition metadata mismatch")
	}
	if msg.Offset > 0 && msg.Offset != record.PartitionOffset {
		return nil, fmt.Errorf("broker event message: offset metadata mismatch")
	}
	payload, err := unmarshalPayload(record.Kind, record.Payload)
	if err != nil {
		return nil, err
	}
	return &proto.Event{
		ID:              record.ID,
		Kind:            record.Kind,
		Seq:             brokerEventSequence(record, msg),
		Payload:         payload,
		TS:              record.TS,
		PartitionKind:   partition.Kind,
		PartitionKey:    partition.Key,
		PartitionOffset: record.PartitionOffset,
		Scopes:          append([]string(nil), record.Scopes...),
	}, nil
}

func brokerEventSequence(record BrokerEventRecord, msg BrokerEventLogMessage) int64 {
	if record.CompatibilitySeq > 0 {
		return record.CompatibilitySeq
	}
	return msg.StreamSeq
}

func eventAppendTS(ts int64) int64 {
	if ts > 0 {
		return ts
	}
	return nowMS()
}

func BrokerEventSubject(partition LogPartition) string {
	partition = partition.Normalize()
	return brokerEventSubjectPrefix + "." + encodeBrokerSubjectToken(partition.Kind) + "." + encodeBrokerSubjectToken(partition.Key)
}

func BrokerEventSubjectWildcard() string {
	return brokerEventSubjectPrefix + ".>"
}

func ParseBrokerEventSubject(subject string) (LogPartition, bool) {
	rest, ok := strings.CutPrefix(subject, brokerEventSubjectPrefix+".")
	if !ok {
		return LogPartition{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return LogPartition{}, false
	}
	kind, err := decodeBrokerSubjectToken(parts[0])
	if err != nil {
		return LogPartition{}, false
	}
	key, err := decodeBrokerSubjectToken(parts[1])
	if err != nil {
		return LogPartition{}, false
	}
	return LogPartition{Kind: kind, Key: key}.Normalize(), true
}

func encodeBrokerSubjectToken(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeBrokerSubjectToken(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// MemoryBrokerEventLogClient is a broker-shaped reference implementation for
// tests and local fixtures. It stores logical partition offsets in the same
// encoded records a real broker backend persists.
type MemoryBrokerEventLogClient struct {
	mu       sync.Mutex
	messages map[LogPartition][]BrokerEventLogMessage
	tails    map[LogPartition]int64
	byID     map[string]BrokerEventLogMessage
	head     int64
}

func NewMemoryBrokerEventLogClient() *MemoryBrokerEventLogClient {
	return &MemoryBrokerEventLogClient{
		messages: map[LogPartition][]BrokerEventLogMessage{},
		tails:    map[LogPartition]int64{},
		byID:     map[string]BrokerEventLogMessage{},
	}
}

func (c *MemoryBrokerEventLogClient) AppendEvent(ctx context.Context, partition LogPartition, record BrokerEventRecord) (BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return BrokerEventLogMessage{}, err
	}
	if c == nil {
		return BrokerEventLogMessage{}, fmt.Errorf("memory broker event log: nil receiver")
	}
	partition = partition.Normalize()

	c.mu.Lock()
	defer c.mu.Unlock()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.ID != "" {
		if existing, ok := c.byID[record.ID]; ok {
			if existing.Partition.Normalize() != partition {
				return BrokerEventLogMessage{}, fmt.Errorf("memory broker event log: duplicate event id %q belongs to %s/%s, not %s/%s",
					record.ID, existing.Partition.Kind, existing.Partition.Key, partition.Kind, partition.Key)
			}
			existingRecord, err := DecodeBrokerEventRecord(existing.Data)
			if err != nil {
				return BrokerEventLogMessage{}, err
			}
			if !sameBrokerEventIdentity(existingRecord, record) {
				return BrokerEventLogMessage{}, fmt.Errorf("memory broker event log: duplicate event id %q has different content", record.ID)
			}
			return cloneBrokerEventLogMessage(existing), nil
		}
	}
	offset := c.tails[partition] + 1
	if record.PartitionOffset > 0 {
		if record.PartitionOffset != offset {
			return BrokerEventLogMessage{}, fmt.Errorf("memory broker event log: partition offset %d for %s/%s must follow current tail %d",
				record.PartitionOffset, partition.Kind, partition.Key, c.tails[partition])
		}
		offset = record.PartitionOffset
	} else {
		record.PartitionOffset = offset
	}
	data, err := EncodeBrokerEventRecord(record)
	if err != nil {
		return BrokerEventLogMessage{}, err
	}
	c.head++
	msg := BrokerEventLogMessage{
		Partition: partition,
		Offset:    offset,
		StreamSeq: c.head,
		Data:      data,
	}
	c.tails[partition] = offset
	c.messages[partition] = append(c.messages[partition], cloneBrokerEventLogMessage(msg))
	if record.ID != "" {
		c.byID[record.ID] = cloneBrokerEventLogMessage(msg)
	}
	return cloneBrokerEventLogMessage(msg), nil
}

func (c *MemoryBrokerEventLogClient) FetchEvents(ctx context.Context, partition LogPartition, afterOffset int64, limit int) ([]BrokerEventLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("memory broker event log: nil receiver")
	}
	partition = partition.Normalize()

	c.mu.Lock()
	defer c.mu.Unlock()
	source := c.messages[partition]
	out := make([]BrokerEventLogMessage, 0, len(source))
	for _, msg := range source {
		if msg.Offset <= afterOffset {
			continue
		}
		out = append(out, cloneBrokerEventLogMessage(msg))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *MemoryBrokerEventLogClient) Head(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil {
		return 0, fmt.Errorf("memory broker event log: nil receiver")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.head, nil
}

func (c *MemoryBrokerEventLogClient) SeedEventPartitionOffset(ctx context.Context, partition LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("memory broker event log: nil receiver")
	}
	if offset < 0 {
		offset = 0
	}
	partition = partition.Normalize()
	c.mu.Lock()
	if offset > c.tails[partition] {
		c.tails[partition] = offset
	}
	c.mu.Unlock()
	return nil
}

func (c *MemoryBrokerEventLogClient) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("memory broker event log: nil receiver")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	partitions := make([]LogPartition, 0, len(c.tails))
	for partition := range c.tails {
		partitions = append(partitions, partition.Normalize())
	}
	sort.Slice(partitions, func(i, j int) bool {
		oi := c.tails[partitions[i]]
		oj := c.tails[partitions[j]]
		if oi == oj {
			if partitions[i].Kind == partitions[j].Kind {
				return partitions[i].Key < partitions[j].Key
			}
			return partitions[i].Kind < partitions[j].Kind
		}
		return oi > oj
	})
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions, nil
}

func (c *MemoryBrokerEventLogClient) ListEventPartitionOffsets(ctx context.Context, limit int) ([]EventPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("memory broker event log: nil receiver")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	offsets := make([]EventPartitionOffset, 0, len(c.tails))
	for partition, tail := range c.tails {
		offsets = append(offsets, EventPartitionOffset{
			Partition:  partition.Normalize(),
			LastOffset: tail,
		})
	}
	sort.Slice(offsets, func(i, j int) bool {
		if offsets[i].LastOffset == offsets[j].LastOffset {
			if offsets[i].Partition.Kind == offsets[j].Partition.Kind {
				return offsets[i].Partition.Key < offsets[j].Partition.Key
			}
			return offsets[i].Partition.Kind < offsets[j].Partition.Kind
		}
		return offsets[i].LastOffset > offsets[j].LastOffset
	})
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func cloneBrokerEventLogMessage(msg BrokerEventLogMessage) BrokerEventLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}

func sameBrokerEventIdentity(existing, requested BrokerEventRecord) bool {
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
