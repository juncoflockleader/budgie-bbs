package natsconn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

type JetStreamCommandEventTransactionOptions struct {
	CommandLog JetStreamCommandLogOptions
	EventLog   JetStreamEventLogOptions
}

type jetStreamCommandCommitter interface {
	CommitPartition(ctx context.Context, partition core.LogPartition, offset int64) error
}

type jetStreamEventAppender interface {
	AppendEvent(ctx context.Context, partition core.LogPartition, record core.BrokerEventRecord) (core.BrokerEventLogMessage, error)
}

type jetStreamEventBatchAppender interface {
	AppendEvents(ctx context.Context, records []core.BrokerEventRecord) ([]core.BrokerEventLogMessage, error)
}

// JetStreamCommandEventTransactionClient implements Budgie's broker transaction
// boundary on top of JetStream's idempotent event appends and command commit
// markers. This is not a cross-stream atomic transaction; it is the staging
// adapter that proves stable event IDs can replay safely if the process crashes
// after appending events but before committing the command offset.
type JetStreamCommandEventTransactionClient struct {
	commands jetStreamCommandCommitter
	events   jetStreamEventAppender
}

var _ core.BrokerCommandEventTransactionClient = (*JetStreamCommandEventTransactionClient)(nil)

func NewJetStreamCommandEventTransactionStore(ctx context.Context, conn *Conn, options JetStreamCommandEventTransactionOptions) (*core.BrokerCommandEventTransactionStore, error) {
	client, err := NewJetStreamCommandEventTransactionClient(ctx, conn, options)
	if err != nil {
		return nil, err
	}
	return core.NewBrokerCommandEventTransactionStore(client), nil
}

func NewJetStreamCommandEventTransactionClient(ctx context.Context, conn *Conn, options JetStreamCommandEventTransactionOptions) (*JetStreamCommandEventTransactionClient, error) {
	commandClient, err := NewJetStreamCommandLogClient(ctx, conn, options.CommandLog)
	if err != nil {
		return nil, err
	}
	eventClient, err := NewJetStreamEventLogClient(ctx, conn, options.EventLog)
	if err != nil {
		return nil, err
	}
	return NewJetStreamCommandEventTransactionClientFromClients(commandClient, eventClient), nil
}

func NewJetStreamCommandEventTransactionClientFromClients(commandClient *JetStreamCommandLogClient, eventClient *JetStreamEventLogClient) *JetStreamCommandEventTransactionClient {
	return newJetStreamCommandEventTransactionClient(commandClient, eventClient)
}

func newJetStreamCommandEventTransactionClient(commands jetStreamCommandCommitter, events jetStreamEventAppender) *JetStreamCommandEventTransactionClient {
	return &JetStreamCommandEventTransactionClient{
		commands: commands,
		events:   events,
	}
}

func (c *JetStreamCommandEventTransactionClient) AppendEventsAndCommitCommand(ctx context.Context, command core.CommandLogCommitPosition, records []core.BrokerEventRecord) (core.BrokerCommandEventTransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	batch, err := c.AppendEventsAndCommitCommands(ctx, []core.CommandLogCommitPosition{command}, records)
	if err != nil {
		return core.BrokerCommandEventTransactionResult{}, err
	}
	if len(batch.Commits) != 1 {
		return core.BrokerCommandEventTransactionResult{}, fmt.Errorf("nats command/event transaction: committed %d commands, want 1", len(batch.Commits))
	}
	return core.BrokerCommandEventTransactionResult{
		Messages:           batch.Messages,
		CommittedPartition: batch.Commits[0].Partition,
		CommittedOffset:    batch.Commits[0].Offset,
	}, nil
}

func (c *JetStreamCommandEventTransactionClient) AppendEventsAndCommitCommands(ctx context.Context, commands []core.CommandLogCommitPosition, records []core.BrokerEventRecord) (core.BrokerCommandEventTransactionBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return core.BrokerCommandEventTransactionBatchResult{}, err
	}
	if c == nil || c.commands == nil || c.events == nil {
		return core.BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("nats command/event transaction: nil client")
	}
	commands = append([]core.CommandLogCommitPosition(nil), commands...)
	for i, command := range commands {
		command = command.Normalize()
		if err := command.Validate(); err != nil {
			return core.BrokerCommandEventTransactionBatchResult{}, fmt.Errorf("nats command/event transaction: command %d: %w", i, err)
		}
		commands[i] = command
	}
	records, err := normalizeJetStreamTransactionEvents(records)
	if err != nil {
		return core.BrokerCommandEventTransactionBatchResult{}, err
	}

	var messages []core.BrokerEventLogMessage
	if appender, ok := c.events.(jetStreamEventBatchAppender); ok {
		messages, err = appender.AppendEvents(ctx, records)
		if err != nil {
			return core.BrokerCommandEventTransactionBatchResult{}, err
		}
	} else {
		messages = make([]core.BrokerEventLogMessage, 0, len(records))
		for _, record := range records {
			partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
			msg, err := c.events.AppendEvent(ctx, partition, record)
			if err != nil {
				return core.BrokerCommandEventTransactionBatchResult{}, err
			}
			messages = append(messages, msg)
		}
	}
	commits := make([]core.CommandLogCommitPosition, 0, len(commands))
	for _, command := range commands {
		if err := c.commands.CommitPartition(ctx, command.Partition, command.Offset); err != nil {
			return core.BrokerCommandEventTransactionBatchResult{}, err
		}
		commits = append(commits, command)
	}
	return core.BrokerCommandEventTransactionBatchResult{
		Messages: messages,
		Commits:  commits,
	}, nil
}

func normalizeJetStreamTransactionEvents(records []core.BrokerEventRecord) ([]core.BrokerEventRecord, error) {
	normalized := make([]core.BrokerEventRecord, 0, len(records))
	byID := map[string]core.BrokerEventRecord{}
	for _, record := range records {
		record.ID = strings.TrimSpace(record.ID)
		if record.ID == "" {
			return nil, fmt.Errorf("nats command/event transaction: event id is required")
		}
		if record.Kind == "" {
			return nil, fmt.Errorf("nats command/event transaction: event kind is required")
		}
		if len(record.Payload) == 0 || !json.Valid(record.Payload) {
			return nil, fmt.Errorf("nats command/event transaction: event payload is not valid JSON")
		}
		partition := core.LogPartition{Kind: record.PartitionKind, Key: record.PartitionKey}.Normalize()
		record.PartitionKind = partition.Kind
		record.PartitionKey = partition.Key
		record.PartitionOffset = 0
		if record.TS <= 0 {
			return nil, fmt.Errorf("nats command/event transaction: event timestamp is required")
		}
		if existing, ok := byID[record.ID]; ok {
			if !sameJetStreamEventIdentity(existing, record) {
				return nil, fmt.Errorf("nats command/event transaction: duplicate event id %q has different content", record.ID)
			}
			return nil, fmt.Errorf("nats command/event transaction: duplicate event id %q in one transaction", record.ID)
		}
		byID[record.ID] = record
		normalized = append(normalized, record)
	}
	return normalized, nil
}
