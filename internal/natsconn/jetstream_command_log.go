package natsconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	nats "github.com/nats-io/nats.go"
)

const (
	DefaultJetStreamCommandLogStream          = "BUDGIE_COMMAND_LOG"
	defaultJetStreamCommandLogStream          = DefaultJetStreamCommandLogStream
	defaultJetStreamCommandLogDuplicateWindow = 24 * time.Hour
	jetStreamCommandLogAppendRetries          = 64
)

type JetStreamCommandLogOptions struct {
	Stream          string
	Replicas        int
	Wait            time.Duration
	DuplicateWindow time.Duration
	ReadOnly        bool
}

func NewJetStreamCommandLog(ctx context.Context, conn *Conn, options JetStreamCommandLogOptions) (*core.BrokerCommandLog, error) {
	client, err := NewJetStreamCommandLogClient(ctx, conn, options)
	if err != nil {
		return nil, err
	}
	return core.NewBrokerCommandLog(client), nil
}

type JetStreamCommandLogClient struct {
	js              nats.JetStreamContext
	stream          string
	replicas        int
	wait            time.Duration
	dupeWindow      time.Duration
	commitTailMu    sync.Mutex
	commitTailCache map[core.LogPartition]jetStreamPartitionTail
}

func NewJetStreamCommandLogClient(ctx context.Context, conn *Conn, options JetStreamCommandLogOptions) (*JetStreamCommandLogClient, error) {
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats command log: nil connection")
	}
	stream := JetStreamName(options.Stream, defaultJetStreamCommandLogStream)
	wait := jetStreamWait(options.Wait)
	replicas := jetStreamReplicas(options.Replicas)
	dupeWindow := jetStreamDuration(options.DuplicateWindow, defaultJetStreamCommandLogDuplicateWindow)
	js, err := conn.nc.JetStream(nats.MaxWait(wait))
	if err != nil {
		return nil, err
	}
	client := &JetStreamCommandLogClient{
		js:              js,
		stream:          stream,
		replicas:        replicas,
		wait:            wait,
		dupeWindow:      dupeWindow,
		commitTailCache: map[core.LogPartition]jetStreamPartitionTail{},
	}
	if options.ReadOnly {
		err = client.validateStream(ctx)
	} else {
		err = client.ensureStream(ctx)
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *JetStreamCommandLogClient) AppendCommand(ctx context.Context, partition core.LogPartition, record core.BrokerCommandRecord) (core.BrokerCommandLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return core.BrokerCommandLogMessage{}, err
	}
	if c == nil || c.js == nil {
		return core.BrokerCommandLogMessage{}, fmt.Errorf("nats command log: nil client")
	}
	partition = partition.Normalize()
	subject := core.BrokerCommandSubject(partition)
	if strings.TrimSpace(record.CID) != "" && record.EnqueuedAt <= 0 {
		return core.BrokerCommandLogMessage{}, fmt.Errorf("nats command log: enqueue time is required when command receipt is set")
	}

	var lastErr error
	for attempt := 0; attempt < jetStreamCommandLogAppendRetries; attempt++ {
		tail, err := c.commandTail(ctx, partition, subject)
		if err != nil {
			return core.BrokerCommandLogMessage{}, err
		}
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		record.Offset = tail.logicalOffset + 1
		if record.CID == "" {
			record.CID = core.SyntheticCommandLogCID(partition, record.Offset)
		}
		if record.EnqueuedAt <= 0 {
			record.EnqueuedAt = time.Now().UnixMilli()
		}
		data, err := core.EncodeBrokerCommandRecord(record)
		if err != nil {
			return core.BrokerCommandLogMessage{}, err
		}
		opts := []nats.PubOpt{
			nats.Context(ctx),
			nats.ExpectStream(c.stream),
			nats.ExpectLastSequencePerSubject(tail.streamSeq),
		}
		if msgID := core.BrokerCommandMessageID(partition, record.ActorID, record.CID); msgID != "" {
			opts = append(opts, nats.MsgId(msgID))
		}
		ack, err := c.js.PublishMsg(&nats.Msg{Subject: subject, Data: data}, opts...)
		if err != nil {
			if isWrongLastSequence(err) {
				lastErr = err
				if err := waitJetStreamCASRetry(ctx, attempt, jetStreamCommandLogAppendRetries); err != nil {
					return core.BrokerCommandLogMessage{}, err
				}
				continue
			}
			return core.BrokerCommandLogMessage{}, err
		}
		if ack.Duplicate && record.CID != "" {
			return c.findCommandByReceipt(ctx, partition, subject, record)
		}
		return core.BrokerCommandLogMessage{
			Partition: partition,
			Offset:    record.Offset,
			StreamSeq: int64(ack.Sequence),
			Data:      data,
		}, nil
	}
	return core.BrokerCommandLogMessage{}, fmt.Errorf("nats command log: append CAS failed after %d attempts: %w", jetStreamCommandLogAppendRetries, lastErr)
}

func (c *JetStreamCommandLogClient) FetchCommands(ctx context.Context, partition core.LogPartition, afterOffset int64, limit int) ([]core.BrokerCommandLogMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats command log: nil client")
	}
	if limit <= 0 {
		limit = 100
	}
	partition = partition.Normalize()
	subject := core.BrokerCommandSubject(partition)
	tail, err := c.commandTail(ctx, partition, subject)
	if err != nil {
		return nil, err
	}
	if tail.logicalOffset <= afterOffset {
		return nil, nil
	}
	want := int(tail.logicalOffset - afterOffset)
	if want > limit {
		want = limit
	}
	out := make([]core.BrokerCommandLogMessage, 0, limit)
	var seq uint64
	for len(out) < want {
		raw, err := c.js.GetMsg(c.stream, seq, nats.Context(ctx), nats.DirectGetNext(subject))
		if errors.Is(err, nats.ErrMsgNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		seq = raw.Sequence + 1
		record, err := core.DecodeBrokerCommandRecord(raw.Data)
		if err != nil {
			return nil, err
		}
		if record.Offset <= afterOffset {
			continue
		}
		out = append(out, core.BrokerCommandLogMessage{
			Partition: partition,
			Offset:    record.Offset,
			StreamSeq: int64(raw.Sequence),
			Data:      append([]byte(nil), raw.Data...),
		})
	}
	return out, nil
}

func (c *JetStreamCommandLogClient) CommitPartition(ctx context.Context, partition core.LogPartition, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.js == nil {
		return fmt.Errorf("nats command log: nil client")
	}
	if offset < 0 {
		offset = 0
	}
	if offset == 0 {
		return nil
	}
	partition = partition.Normalize()
	subject := core.BrokerCommandCommitSubject(partition)
	tail, haveTail := c.cachedCommitTail(partition)
	var lastErr error
	for attempt := 0; attempt < jetStreamCommandLogAppendRetries; attempt++ {
		if haveTail && offset <= tail.logicalOffset {
			return nil
		}
		data, err := encodeCommandCommitRecord(commandCommitRecord{
			Version:       1,
			PartitionKind: partition.Kind,
			PartitionKey:  partition.Key,
			Offset:        offset,
			TS:            time.Now().UnixMilli(),
		})
		if err != nil {
			return err
		}
		ack, err := c.js.PublishMsg(&nats.Msg{Subject: subject, Data: data},
			nats.Context(ctx),
			nats.ExpectStream(c.stream),
			nats.ExpectLastSequencePerSubject(tail.streamSeq),
		)
		if err != nil {
			if isWrongLastSequence(err) {
				lastErr = err
				c.forgetCachedCommitTail(partition)
				tail, err = c.commitTail(ctx, partition, subject)
				if err != nil {
					return err
				}
				haveTail = true
				if offset <= tail.logicalOffset {
					c.rememberCommitTail(partition, tail)
					return nil
				}
				if err := waitJetStreamCASRetry(ctx, attempt, jetStreamCommandLogAppendRetries); err != nil {
					return err
				}
				continue
			}
			return err
		}
		c.rememberCommitTail(partition, jetStreamPartitionTail{streamSeq: ack.Sequence, logicalOffset: offset})
		return nil
	}
	return fmt.Errorf("nats command log: commit CAS failed after %d attempts: %w", jetStreamCommandLogAppendRetries, lastErr)
}

func (c *JetStreamCommandLogClient) CommittedOffset(ctx context.Context, partition core.LogPartition) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c == nil || c.js == nil {
		return 0, fmt.Errorf("nats command log: nil client")
	}
	tail, err := c.commitTail(ctx, partition.Normalize(), core.BrokerCommandCommitSubject(partition))
	if err != nil {
		return 0, err
	}
	return tail.logicalOffset, nil
}

func (c *JetStreamCommandLogClient) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats command log: nil client")
	}
	info, err := c.js.StreamInfo(c.stream, &nats.StreamInfoRequest{SubjectsFilter: core.BrokerCommandSubjectWildcard()}, nats.Context(ctx))
	if err != nil {
		return nil, err
	}
	offsets := make([]core.CommandPartitionOffset, 0, len(info.State.Subjects))
	for subject, count := range info.State.Subjects {
		partition, ok := core.ParseBrokerCommandSubject(subject)
		if !ok {
			continue
		}
		offsets = append(offsets, core.CommandPartitionOffset{
			Partition:  partition,
			TailOffset: int64(count),
		})
	}
	return core.CommandPartitionsByTailOffset(offsets, limit), nil
}

func (c *JetStreamCommandLogClient) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]core.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.js == nil {
		return nil, fmt.Errorf("nats command log: nil client")
	}
	commandInfo, err := c.js.StreamInfo(c.stream, &nats.StreamInfoRequest{SubjectsFilter: core.BrokerCommandSubjectWildcard()}, nats.Context(ctx))
	if err != nil {
		return nil, err
	}
	commitInfo, err := c.js.StreamInfo(c.stream, &nats.StreamInfoRequest{SubjectsFilter: core.BrokerCommandCommitSubjectWildcard()}, nats.Context(ctx))
	if err != nil {
		return nil, err
	}
	commitSubjects := make(map[core.LogPartition]string, len(commitInfo.State.Subjects))
	for subject, count := range commitInfo.State.Subjects {
		if count == 0 {
			continue
		}
		partition, ok := core.ParseBrokerCommandCommitSubject(subject)
		if !ok {
			continue
		}
		commitSubjects[partition.Normalize()] = subject
	}
	offsets := make([]core.CommandPartitionOffset, 0, len(commandInfo.State.Subjects))
	for subject, count := range commandInfo.State.Subjects {
		if count == 0 {
			continue
		}
		partition, ok := core.ParseBrokerCommandSubject(subject)
		if !ok {
			continue
		}
		partition = partition.Normalize()
		committedOffset := int64(0)
		if commitSubject := commitSubjects[partition]; commitSubject != "" {
			commitTail, err := c.commitTailFromExistingSubject(ctx, partition, commitSubject)
			if err != nil {
				return nil, err
			}
			committedOffset = commitTail.logicalOffset
		}
		offsets = append(offsets, core.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      int64(count),
			CommittedOffset: committedOffset,
		})
	}
	for partition, subject := range commitSubjects {
		if _, ok := commandInfo.State.Subjects[core.BrokerCommandSubject(partition)]; ok {
			continue
		}
		commitTail, err := c.commitTailFromExistingSubject(ctx, partition, subject)
		if err != nil {
			return nil, err
		}
		offsets = append(offsets, core.CommandPartitionOffset{
			Partition:       partition,
			TailOffset:      0,
			CommittedOffset: commitTail.logicalOffset,
		})
	}
	core.SortCommandPartitionOffsetsByLag(offsets)
	if limit > 0 && len(offsets) > limit {
		offsets = offsets[:limit]
	}
	return offsets, nil
}

func (c *JetStreamCommandLogClient) ensureStream(ctx context.Context) error {
	cfg := JetStreamCommandLogStreamConfig(JetStreamCommandLogOptions{
		Stream:          c.stream,
		Replicas:        c.replicas,
		DuplicateWindow: c.dupeWindow,
	})
	return ensureJetStreamStream(ctx, c.js, cfg)
}

func (c *JetStreamCommandLogClient) validateStream(ctx context.Context) error {
	return validateJetStreamStream(ctx, c.js, c.stream, "nats command log", []string{
		core.BrokerCommandSubjectWildcard(),
		core.BrokerCommandCommitSubjectWildcard(),
	})
}

func (c *JetStreamCommandLogClient) findCommandByReceipt(ctx context.Context, partition core.LogPartition, subject string, requested core.BrokerCommandRecord) (core.BrokerCommandLogMessage, error) {
	var seq uint64
	for {
		raw, err := c.js.GetMsg(c.stream, seq, nats.Context(ctx), nats.DirectGetNext(subject))
		if errors.Is(err, nats.ErrMsgNotFound) {
			break
		}
		if err != nil {
			return core.BrokerCommandLogMessage{}, err
		}
		seq = raw.Sequence + 1
		record, err := core.DecodeBrokerCommandRecord(raw.Data)
		if err != nil {
			return core.BrokerCommandLogMessage{}, err
		}
		if record.ActorID != requested.ActorID || record.CID != requested.CID {
			continue
		}
		if !sameJetStreamCommandIdentity(record, requested) {
			return core.BrokerCommandLogMessage{}, fmt.Errorf("nats command log: duplicate command receipt %q has different content", requested.CID)
		}
		return core.BrokerCommandLogMessage{
			Partition: partition.Normalize(),
			Offset:    record.Offset,
			StreamSeq: int64(raw.Sequence),
			Data:      append([]byte(nil), raw.Data...),
		}, nil
	}
	return core.BrokerCommandLogMessage{}, fmt.Errorf("nats command log: duplicate command receipt %q was not found in partition %s/%s",
		requested.CID, partition.Kind, partition.Key)
}

func (c *JetStreamCommandLogClient) commandTail(ctx context.Context, partition core.LogPartition, subject string) (jetStreamPartitionTail, error) {
	raw, err := c.js.GetLastMsg(c.stream, subject, nats.Context(ctx), nats.DirectGet())
	if errors.Is(err, nats.ErrMsgNotFound) {
		return jetStreamPartitionTail{}, nil
	}
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	record, err := core.DecodeBrokerCommandRecord(raw.Data)
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	if got := (core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()); got != partition.Normalize() {
		return jetStreamPartitionTail{}, fmt.Errorf("nats command log: command partition mismatch %s/%s for %s/%s", got.Kind, got.Key, partition.Kind, partition.Key)
	}
	return jetStreamPartitionTail{streamSeq: raw.Sequence, logicalOffset: record.Offset}, nil
}

func (c *JetStreamCommandLogClient) commitTail(ctx context.Context, partition core.LogPartition, subject string) (jetStreamPartitionTail, error) {
	hasMessages, err := c.subjectHasMessages(ctx, subject)
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	if !hasMessages {
		return jetStreamPartitionTail{}, nil
	}
	return c.commitTailFromExistingSubject(ctx, partition, subject)
}

func (c *JetStreamCommandLogClient) commitTailFromExistingSubject(ctx context.Context, partition core.LogPartition, subject string) (jetStreamPartitionTail, error) {
	raw, err := c.js.GetLastMsg(c.stream, subject, nats.Context(ctx), nats.DirectGet())
	if errors.Is(err, nats.ErrMsgNotFound) {
		return jetStreamPartitionTail{}, nil
	}
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	record, err := decodeCommandCommitRecord(raw.Data)
	if err != nil {
		return jetStreamPartitionTail{}, err
	}
	if got := (core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()); got != partition.Normalize() {
		return jetStreamPartitionTail{}, fmt.Errorf("nats command log: commit partition mismatch %s/%s for %s/%s", got.Kind, got.Key, partition.Kind, partition.Key)
	}
	return jetStreamPartitionTail{streamSeq: raw.Sequence, logicalOffset: record.Offset}, nil
}

func (c *JetStreamCommandLogClient) cachedCommitTail(partition core.LogPartition) (jetStreamPartitionTail, bool) {
	if c == nil {
		return jetStreamPartitionTail{}, false
	}
	partition = partition.Normalize()
	c.commitTailMu.Lock()
	defer c.commitTailMu.Unlock()
	tail, ok := c.commitTailCache[partition]
	return tail, ok
}

func (c *JetStreamCommandLogClient) rememberCommitTail(partition core.LogPartition, tail jetStreamPartitionTail) {
	if c == nil {
		return
	}
	partition = partition.Normalize()
	c.commitTailMu.Lock()
	defer c.commitTailMu.Unlock()
	if c.commitTailCache == nil {
		c.commitTailCache = map[core.LogPartition]jetStreamPartitionTail{}
	}
	c.commitTailCache[partition] = tail
}

func (c *JetStreamCommandLogClient) forgetCachedCommitTail(partition core.LogPartition) {
	if c == nil {
		return
	}
	partition = partition.Normalize()
	c.commitTailMu.Lock()
	defer c.commitTailMu.Unlock()
	delete(c.commitTailCache, partition)
}

func (c *JetStreamCommandLogClient) subjectHasMessages(ctx context.Context, subject string) (bool, error) {
	info, err := c.js.StreamInfo(c.stream, &nats.StreamInfoRequest{SubjectsFilter: subject}, nats.Context(ctx))
	if err != nil {
		return false, err
	}
	for got, count := range info.State.Subjects {
		if got == subject && count > 0 {
			return true, nil
		}
	}
	return false, nil
}

type commandCommitRecord struct {
	Version       int    `json:"v"`
	PartitionKind string `json:"partitionKind"`
	PartitionKey  string `json:"partitionKey"`
	Offset        int64  `json:"offset"`
	TS            int64  `json:"ts"`
}

func encodeCommandCommitRecord(record commandCommitRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = 1
	}
	if record.Version != 1 {
		return nil, fmt.Errorf("nats command log: unsupported commit version %d", record.Version)
	}
	partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.Offset < 0 {
		return nil, fmt.Errorf("nats command log: negative committed offset")
	}
	if record.TS <= 0 {
		record.TS = time.Now().UnixMilli()
	}
	return json.Marshal(record)
}

func decodeCommandCommitRecord(data []byte) (commandCommitRecord, error) {
	var record commandCommitRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return commandCommitRecord{}, err
	}
	if record.Version != 1 {
		return commandCommitRecord{}, fmt.Errorf("nats command log: unsupported commit version %d", record.Version)
	}
	partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
	record.PartitionKind = partition.Kind
	record.PartitionKey = partition.Key
	if record.Offset < 0 {
		return commandCommitRecord{}, fmt.Errorf("nats command log: negative committed offset")
	}
	return record, nil
}

func sameJetStreamCommandIdentity(existing, requested core.BrokerCommandRecord) bool {
	return existing.ActorID == requested.ActorID &&
		existing.CID == requested.CID &&
		existing.Command == requested.Command &&
		existing.EnqueuedAt == requested.EnqueuedAt &&
		existing.PartitionKind == requested.PartitionKind &&
		existing.PartitionKey == requested.PartitionKey &&
		string(existing.Payload) == string(requested.Payload)
}
