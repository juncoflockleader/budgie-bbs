package kafkaconn

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

type CommandWriterClientOptions struct {
	Runtime               RuntimeConfig
	ClientID              string
	TransactionalID       string
	CommandPartitionCount int32
	Assignment            CommandPartitionAssignmentSnapshotApplier
	Candidates            core.CommandPartitionLister
	CandidateLimit        int
}

type RuntimeClientOptions struct {
	Runtime  RuntimeConfig
	ClientID string
}

type CommandLogRuntimeClientOptions = RuntimeClientOptions
type CommandLogProducerRuntimeClientOptions = RuntimeClientOptions
type EventLogRuntimeClientOptions = RuntimeClientOptions
type EventLogProducerRuntimeClientOptions = RuntimeClientOptions

func NewCommandLogRuntimeClient(ctx context.Context, options CommandLogRuntimeClientOptions) (*kgo.Client, error) {
	return newKafkaClient(ctx, options, CommandLogRuntimeClientOpts)
}

func NewEventLogRuntimeClient(ctx context.Context, options EventLogRuntimeClientOptions) (*kgo.Client, error) {
	return newKafkaClient(ctx, options, EventLogRuntimeClientOpts)
}

func NewCommandLogProducerRuntimeClient(ctx context.Context, options CommandLogProducerRuntimeClientOptions) (*kgo.Client, error) {
	return newKafkaClient(ctx, options, CommandLogProducerRuntimeClientOpts)
}

func NewEventLogProducerRuntimeClient(ctx context.Context, options EventLogProducerRuntimeClientOptions) (*kgo.Client, error) {
	return newKafkaClient(ctx, options, EventLogProducerRuntimeClientOpts)
}

func CommandLogRuntimeClientOpts(options CommandLogRuntimeClientOptions) ([]kgo.Opt, error) {
	runtime, _, opts, err := validatedRuntimeClientOpts("command log client", options.Runtime, options.ClientID, RuntimeConfig.ValidateCommandLog)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.ConsumeTopics(runtime.CommandTopic),
		kgo.ConsumerGroup(runtime.ConsumerGroup),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	)
	return opts, nil
}

func CommandLogProducerRuntimeClientOpts(options CommandLogProducerRuntimeClientOptions) ([]kgo.Opt, error) {
	_, _, opts, err := validatedRuntimeClientOpts("command log producer", options.Runtime, options.ClientID, RuntimeConfig.ValidateCommandLog)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	return opts, nil
}

func EventLogRuntimeClientOpts(options EventLogRuntimeClientOptions) ([]kgo.Opt, error) {
	runtime, _, opts, err := validatedRuntimeClientOpts("event log client", options.Runtime, options.ClientID, RuntimeConfig.ValidateEventLog)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.ConsumeTopics(runtime.EventTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	return opts, nil
}

func EventLogProducerRuntimeClientOpts(options EventLogProducerRuntimeClientOptions) ([]kgo.Opt, error) {
	_, _, opts, err := validatedRuntimeClientOpts("event log producer", options.Runtime, options.ClientID, RuntimeConfig.ValidateEventLog)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	return opts, nil
}

func NewCommandWriterClient(ctx context.Context, options CommandWriterClientOptions) (*kgo.Client, error) {
	return newKafkaClient(ctx, options, CommandWriterClientOpts)
}

func NewCommandWriterTransactionSession(ctx context.Context, options CommandWriterClientOptions) (*kgo.GroupTransactSession, error) {
	return newKafkaTransactionSession(ctx, options, CommandWriterClientOpts)
}

func newKafkaClient[O any](ctx context.Context, options O, optsFor func(O) ([]kgo.Opt, error)) (*kgo.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := optsFor(options)
	if err != nil {
		return nil, err
	}
	return kgo.NewClient(opts...)
}

func newKafkaTransactionSession[O any](ctx context.Context, options O, optsFor func(O) ([]kgo.Opt, error)) (*kgo.GroupTransactSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := optsFor(options)
	if err != nil {
		return nil, err
	}
	return kgo.NewGroupTransactSession(opts...)
}

func CommandWriterClientOpts(options CommandWriterClientOptions) ([]kgo.Opt, error) {
	runtime, clientID, opts, err := validatedRuntimeClientOpts("command writer client", options.Runtime, options.ClientID, RuntimeConfig.ValidateCommandEventTransaction)
	if err != nil {
		return nil, err
	}
	transactionalID := strings.TrimSpace(options.TransactionalID)
	if transactionalID == "" {
		return nil, fmt.Errorf("kafka command writer client: transactional id is required")
	}
	if options.Assignment != nil && options.CommandPartitionCount <= 0 {
		return nil, fmt.Errorf("kafka command writer client: command topic partition count is required for assignment callbacks")
	}

	callbacks := newCommandWriterRebalanceCallbacks(
		NewCommandPartitionRebalanceAdapter(options.Assignment, CommandPartitionAssignmentOptions{
			CommandTopic:   runtime.CommandTopic,
			OwnerID:        clientID,
			PartitionCount: options.CommandPartitionCount,
		}),
		options.Candidates,
		options.CandidateLimit,
	)

	opts = append(opts,
		kgo.ConsumeTopics(runtime.CommandTopic),
		kgo.ConsumerGroup(runtime.ConsumerGroup),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.TransactionalID(transactionalID),
	)
	if options.Assignment != nil {
		if options.Candidates == nil {
			return nil, fmt.Errorf("kafka command writer client: assignment callbacks require command partition candidates")
		}
		opts = append(opts,
			kgo.OnPartitionsAssigned(callbacks.onPartitionsAssigned),
			kgo.OnPartitionsRevoked(callbacks.onPartitionsRevoked),
			kgo.OnPartitionsLost(callbacks.onPartitionsRevoked),
		)
	}
	return opts, nil
}

func validatedRuntimeClientOpts(label string, runtime RuntimeConfig, clientID string, validate func(RuntimeConfig) error) (RuntimeConfig, string, []kgo.Opt, error) {
	runtime = runtime.Normalize()
	if err := validate(runtime); err != nil {
		return runtime, "", nil, err
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return runtime, "", nil, fmt.Errorf("kafka %s: client id is required", label)
	}
	opts, err := RuntimeClientOpts(runtime, clientID)
	return runtime, clientID, opts, err
}

type commandWriterRebalanceCallbacks struct {
	assignment         *CommandPartitionRebalanceAdapter
	candidates         core.CommandPartitionLister
	candidateLimit     int
	fallbackGeneration atomic.Int64
}

func newCommandWriterRebalanceCallbacks(assignment *CommandPartitionRebalanceAdapter, candidates core.CommandPartitionLister, candidateLimit int) *commandWriterRebalanceCallbacks {
	return &commandWriterRebalanceCallbacks{
		assignment:     assignment,
		candidates:     candidates,
		candidateLimit: candidateLimit,
	}
}

func (c *commandWriterRebalanceCallbacks) onPartitionsAssigned(ctx context.Context, client *kgo.Client, topicPartitions map[string][]int32) {
	if c == nil || c.assignment == nil || c.candidates == nil {
		return
	}
	generation := c.generation(client)
	candidates, err := c.candidates.ListCommandPartitions(ctx, c.candidateLimit)
	if err != nil {
		_, _ = c.assignment.RevokeConsumerGroupAssignment(ctx, generation)
		return
	}
	if _, err := c.assignment.ApplyConsumerGroupAssignment(ctx, generation, topicPartitionAssignmentsFromMap(topicPartitions), candidates); err != nil {
		_, _ = c.assignment.RevokeConsumerGroupAssignment(ctx, generation)
	}
}

func (c *commandWriterRebalanceCallbacks) onPartitionsRevoked(ctx context.Context, client *kgo.Client, _ map[string][]int32) {
	if c == nil || c.assignment == nil {
		return
	}
	_, _ = c.assignment.RevokeConsumerGroupAssignment(ctx, c.generation(client))
}

func (c *commandWriterRebalanceCallbacks) generation(client *kgo.Client) int64 {
	if client != nil {
		if _, generation := client.GroupMetadata(); generation > 0 {
			// Snapshot assigners start at generation 1, so offset broker
			// generations by one to ensure the first live callback advances.
			return int64(generation) + 1
		}
	}
	return c.fallbackGeneration.Add(1) + 1
}

func topicPartitionAssignmentsFromMap(topicPartitions map[string][]int32) []TopicPartitionAssignment {
	topics := make([]string, 0, len(topicPartitions))
	for topic := range topicPartitions {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	out := make([]TopicPartitionAssignment, 0)
	for _, topic := range topics {
		partitions := append([]int32(nil), topicPartitions[topic]...)
		sort.Slice(partitions, func(i, j int) bool {
			return partitions[i] < partitions[j]
		})
		for _, partition := range partitions {
			out = append(out, TopicPartitionAssignment{
				Topic:     topic,
				Partition: partition,
			})
		}
	}
	return out
}
