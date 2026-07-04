package kafkaconn

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestCommandWriterClientOptsValidateFranzConfig(t *testing.T) {
	opts, err := CommandWriterClientOpts(CommandWriterClientOptions{
		Runtime:         RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		ClientID:        "writer-a",
		TransactionalID: "budgie-writer-a",
	})
	if err != nil {
		t.Fatalf("CommandWriterClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
}

func TestCommandLogRuntimeClientOptsValidateFranzConfig(t *testing.T) {
	opts, err := CommandLogRuntimeClientOpts(CommandLogRuntimeClientOptions{
		Runtime:  RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.commands", ""),
		ClientID: "command-log-client",
	})
	if err != nil {
		t.Fatalf("CommandLogRuntimeClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
}

func TestCommandLogProducerRuntimeClientOptsValidateFranzConfig(t *testing.T) {
	opts, err := CommandLogProducerRuntimeClientOpts(CommandLogProducerRuntimeClientOptions{
		Runtime:  RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.commands", ""),
		ClientID: "command-log-producer",
	})
	if err != nil {
		t.Fatalf("CommandLogProducerRuntimeClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
}

func TestEventLogRuntimeClientOptsValidateFranzConfig(t *testing.T) {
	opts, err := EventLogRuntimeClientOpts(EventLogRuntimeClientOptions{
		Runtime:  RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		ClientID: "event-log-client",
	})
	if err != nil {
		t.Fatalf("EventLogRuntimeClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
}

func TestEventLogProducerRuntimeClientOptsValidateFranzConfig(t *testing.T) {
	opts, err := EventLogProducerRuntimeClientOpts(EventLogProducerRuntimeClientOptions{
		Runtime:  RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""),
		ClientID: "event-log-shadow",
	})
	if err != nil {
		t.Fatalf("EventLogProducerRuntimeClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
}

func TestRuntimeClientOptsIncludeKafkaSecurity(t *testing.T) {
	config := RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		TLS:           true,
		TLSServerName: "redpanda.staging.internal",
		SASLMechanism: "scram-sha-256",
		SASLUser:      "budgie",
		SASLPassword:  "secret",
	})
	opts, err := RuntimeClientOpts(config, "secure-client")
	if err != nil {
		t.Fatalf("RuntimeClientOpts: %v", err)
	}
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatalf("kgo.ValidateOpts: %v", err)
	}
	if len(opts) < 4 {
		t.Fatalf("RuntimeClientOpts produced %d opts, want seed, client id, TLS, SASL", len(opts))
	}
}

func TestRuntimeClientOptsRejectInvalidSecurity(t *testing.T) {
	_, err := RuntimeClientOpts(RuntimeConfigFromOptions("redpanda:9092", "", "", "", RuntimeSecurityConfig{
		SASLMechanism: "plain",
		SASLUser:      "budgie",
	}), "secure-client")
	requireErrorContains(t, err, "SASL password is required")
}

func TestCommandLogRuntimeClientOptsRequiresClientIDAndBrokers(t *testing.T) {
	_, err := CommandLogRuntimeClientOpts(CommandLogRuntimeClientOptions{
		Runtime: RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
	})
	requireErrorContains(t, err, "client id is required")
	_, err = CommandLogRuntimeClientOpts(CommandLogRuntimeClientOptions{
		Runtime:  RuntimeConfig{},
		ClientID: "command-log-client",
	})
	requireErrorContains(t, err, "broker list is required")
}

func TestCommandLogProducerRuntimeClientOptsRequiresClientIDAndBrokers(t *testing.T) {
	_, err := CommandLogProducerRuntimeClientOpts(CommandLogProducerRuntimeClientOptions{
		Runtime: RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
	})
	requireErrorContains(t, err, "client id is required")
	_, err = CommandLogProducerRuntimeClientOpts(CommandLogProducerRuntimeClientOptions{
		Runtime:  RuntimeConfig{},
		ClientID: "command-log-producer",
	})
	requireErrorContains(t, err, "broker list is required")
}

func TestEventLogRuntimeClientOptsRequiresClientIDAndBrokers(t *testing.T) {
	_, err := EventLogRuntimeClientOpts(EventLogRuntimeClientOptions{
		Runtime: RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
	})
	requireErrorContains(t, err, "client id is required")
	_, err = EventLogRuntimeClientOpts(EventLogRuntimeClientOptions{
		Runtime:  RuntimeConfig{},
		ClientID: "event-log-client",
	})
	requireErrorContains(t, err, "broker list is required")
}

func TestEventLogProducerRuntimeClientOptsRequiresClientIDAndBrokers(t *testing.T) {
	_, err := EventLogProducerRuntimeClientOpts(EventLogProducerRuntimeClientOptions{
		Runtime: RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
	})
	requireErrorContains(t, err, "client id is required")
	_, err = EventLogProducerRuntimeClientOpts(EventLogProducerRuntimeClientOptions{
		Runtime:  RuntimeConfig{},
		ClientID: "event-log-shadow",
	})
	requireErrorContains(t, err, "broker list is required")
}

func TestCommandWriterClientOptsRequiresWriterIdentity(t *testing.T) {
	_, err := CommandWriterClientOpts(CommandWriterClientOptions{
		Runtime:         RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		TransactionalID: "budgie-writer-a",
	})
	requireErrorContains(t, err, "client id is required")
	_, err = CommandWriterClientOpts(CommandWriterClientOptions{
		Runtime:  RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		ClientID: "writer-a",
	})
	requireErrorContains(t, err, "transactional id is required")
}

func TestCommandWriterClientOptsRequiresPartitionCountForAssignmentCallbacks(t *testing.T) {
	_, err := CommandWriterClientOpts(CommandWriterClientOptions{
		Runtime:         RuntimeConfigFromFlags("redpanda:9092", "", "", ""),
		ClientID:        "writer-a",
		TransactionalID: "budgie-writer-a",
		Assignment:      core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{}),
		Candidates:      staticCommandPartitionCandidates{},
	})
	requireErrorContains(t, err, "command topic partition count is required")
}

func requireErrorContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want containing %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want containing %q", err, want)
		}
	}
}

func TestCommandWriterRebalanceCallbacksApplyAssignmentAndRevoke(t *testing.T) {
	ctx := context.Background()
	commandTopic := "budgie.commands"
	partitionCount := int32(32)
	ownedLogical := core.LogPartition{Kind: "board", Key: "general"}
	ownedPhysical, err := KafkaPartitionForLogicalPartition(ownedLogical, partitionCount)
	if err != nil {
		t.Fatalf("owned physical partition: %v", err)
	}
	assigner := core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{})
	callbacks := newCommandWriterRebalanceCallbacks(
		NewCommandPartitionRebalanceAdapter(assigner, CommandPartitionAssignmentOptions{
			CommandTopic:   commandTopic,
			OwnerID:        "writer-a",
			PartitionCount: partitionCount,
		}),
		staticCommandPartitionCandidates{ownedLogical},
		100,
	)

	callbacks.onPartitionsAssigned(ctx, nil, map[string][]int32{
		commandTopic: {ownedPhysical},
	})
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign after callback: %v", err)
	}
	if !assigned || assignment.OwnerID != "writer-a" || assignment.Generation != 2 {
		t.Fatalf("assignment after callback = %+v assigned=%v, want writer-a generation 2", assignment, assigned)
	}

	callbacks.onPartitionsRevoked(ctx, nil, nil)
	assignment, assigned, err = assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign after revoke: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 3 {
		t.Fatalf("assignment after revoke = %+v assigned=%v, want fail-closed generation 3", assignment, assigned)
	}
}

func TestCommandWriterRebalanceCallbacksFailClosedWhenCandidatesFail(t *testing.T) {
	ctx := context.Background()
	commandTopic := "budgie.commands"
	partitionCount := int32(32)
	ownedLogical := core.LogPartition{Kind: "board", Key: "general"}
	assigner := core.NewSnapshotCommandPartitionAssigner(core.CommandPartitionAssignmentSnapshot{
		Generation: 1,
		Owners: map[core.LogPartition]string{
			ownedLogical: "writer-a",
		},
	})
	callbacks := newCommandWriterRebalanceCallbacks(
		NewCommandPartitionRebalanceAdapter(assigner, CommandPartitionAssignmentOptions{
			CommandTopic:   commandTopic,
			OwnerID:        "writer-a",
			PartitionCount: partitionCount,
		}),
		failingCommandPartitionCandidates{},
		100,
	)

	callbacks.onPartitionsAssigned(ctx, nil, map[string][]int32{
		commandTopic: {0},
	})
	assignment, assigned, err := assigner.AssignCommandPartition(ctx, "writer-a", ownedLogical)
	if err != nil {
		t.Fatalf("assign after failed callback: %v", err)
	}
	if assigned || assignment.OwnerID != "" || assignment.Generation != 2 {
		t.Fatalf("assignment after failed callback = %+v assigned=%v, want fail-closed generation 2", assignment, assigned)
	}
}

func TestTopicPartitionAssignmentsFromMapSortsForDeterminism(t *testing.T) {
	got := topicPartitionAssignmentsFromMap(map[string][]int32{
		"topic-b": {3, 1},
		"topic-a": {2},
	})
	want := []TopicPartitionAssignment{
		{Topic: "topic-a", Partition: 2},
		{Topic: "topic-b", Partition: 1},
		{Topic: "topic-b", Partition: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignments = %+v, want %+v", got, want)
	}
}

func TestKafkaPartitionForKeyMatchesFranzStickyKeyPartitioner(t *testing.T) {
	partition := core.LogPartition{Kind: "board", Key: "general"}
	key := LogicalPartitionKey(partition)
	partitionCount := int32(32)
	want, err := KafkaPartitionForKey(key, partitionCount)
	if err != nil {
		t.Fatalf("KafkaPartitionForKey: %v", err)
	}
	got := kgo.StickyKeyPartitioner(nil).ForTopic(DefaultCommandTopic).Partition(&kgo.Record{
		Key: []byte(key),
	}, int(partitionCount))
	if int32(got) != want {
		t.Fatalf("franz-go partition = %d, want translated Kafka partition %d", got, want)
	}
}

type staticCommandPartitionCandidates []core.LogPartition

func (c staticCommandPartitionCandidates) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := append([]core.LogPartition(nil), c...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type failingCommandPartitionCandidates struct{}

func (failingCommandPartitionCandidates) ListCommandPartitions(ctx context.Context, limit int) ([]core.LogPartition, error) {
	return nil, context.Canceled
}
