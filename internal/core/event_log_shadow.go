package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type eventLogShadowBus struct {
	Bus
	primary  EventStore
	shadow   EventStore
	reporter EventParityReporter
}

func newEventLogShadowBus(bus Bus, primary, shadow EventStore, reporter EventParityReporter) Bus {
	if bus == nil || primary == nil || shadow == nil {
		return bus
	}
	return &eventLogShadowBus{
		Bus:      bus,
		primary:  primary,
		shadow:   shadow,
		reporter: defaultEventParityReporter(reporter),
	}
}

func (b *eventLogShadowBus) Start(ctx context.Context) error {
	if starter, ok := b.Bus.(interface{ Start(context.Context) error }); ok {
		return starter.Start(ctx)
	}
	return nil
}

func (b *eventLogShadowBus) Publish(evt *proto.Event) {
	b.Bus.Publish(evt)
	b.mirror(evt)
}

func (b *eventLogShadowBus) PublishLocal(evt *proto.Event) {
	if local, ok := b.Bus.(localOnlyBus); ok {
		local.PublishLocal(evt)
	} else {
		b.Bus.Publish(evt)
	}
	b.mirror(evt)
}

func (b *eventLogShadowBus) mirror(evt *proto.Event) {
	if evt == nil || !evt.IsDurable() || evt.Seq <= 0 {
		return
	}
	primary, err := fetchEventBySeq(context.Background(), b.primary, evt.Seq)
	if err != nil {
		recordEventParityIssue(b.reporter, EventParityIssue{
			Kind:       EventParityReplayError,
			Event:      evt.Kind,
			PrimarySeq: evt.Seq,
			Partition:  logPartitionFromEvent(evt),
			Message:    "primary event replay failed before shadow append",
			Err:        err.Error(),
		})
		return
	}
	shadow, err := b.shadow.Append(context.Background(), eventAppendFromEvent(primary))
	if err != nil {
		recordEventParityIssue(b.reporter, EventParityIssue{
			Kind:          EventParityAppendError,
			Event:         primary.Kind,
			Partition:     logPartitionFromEvent(primary),
			PrimarySeq:    primary.Seq,
			PrimaryOffset: primary.PartitionOffset,
			Message:       "shadow append failed",
			Err:           err.Error(),
		})
		return
	}
	if issue, ok := compareEventParity(primary, shadow); ok {
		recordEventParityIssue(b.reporter, issue)
	}
}

func fetchEventBySeq(ctx context.Context, store EventStore, seq int64) (*proto.Event, error) {
	events, err := store.Replay(ctx, seq-1, nil, 1)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 || events[0].Seq != seq {
		return nil, fmt.Errorf("seq %d not replayable", seq)
	}
	return events[0], nil
}

func (c *Core) StartEventLogShadowParity(ctx context.Context) *EventReplayParityRunner {
	if c == nil || c.eventLogShadow == nil || c.DB == nil {
		return nil
	}
	primary := NewSQLEventStore(c.DB)
	reporter := defaultEventParityReporter(c.eventLogShadowReporter)
	runner := NewEventReplayParityRunner(EventReplayParityRunnerConfig{
		Primary:        primary,
		Shadow:         c.eventLogShadow,
		Partitions:     primary,
		Reporter:       reporter,
		ReplayLimit:    c.eventLogShadowReplay,
		PartitionLimit: c.eventLogShadowPartition,
		Interval:       c.eventLogShadowInterval,
	})
	if c.eventLogShadowStartHead {
		if err := seedShadowParityAtHead(ctx, primary, c.eventLogShadow, runner, c.eventLogShadowPartition); err != nil {
			slog.Warn("event log shadow: seed at head failed", "err", err)
		}
	}
	go runner.Run(ctx)
	return runner
}

func seedShadowParityAtHead(ctx context.Context, primary *SQLEventStore, shadow EventStore, runner *EventReplayParityRunner, limit int) error {
	if primary == nil || runner == nil {
		return nil
	}
	offsets, err := primary.ListEventPartitionOffsets(ctx, limit)
	if err != nil {
		return err
	}
	seeder, _ := shadow.(EventPartitionOffsetSeeder)
	for _, offset := range offsets {
		if seeder != nil {
			if err := seeder.SeedEventPartitionOffset(ctx, offset.Partition, offset.LastOffset); err != nil {
				return err
			}
		}
		runner.SetCheckpoint(offset.Partition, offset.LastOffset)
	}
	return nil
}

type slogEventParityReporter struct{}

func defaultEventParityReporter(reporter EventParityReporter) EventParityReporter {
	if reporter != nil {
		return reporter
	}
	return slogEventParityReporter{}
}

func (slogEventParityReporter) RecordEventParityIssue(issue EventParityIssue) {
	slog.Warn("event log shadow parity issue",
		"kind", issue.Kind,
		"event", issue.Event,
		"partitionKind", issue.Partition.Kind,
		"partitionKey", issue.Partition.Key,
		"primarySeq", issue.PrimarySeq,
		"shadowSeq", issue.ShadowSeq,
		"primaryOffset", issue.PrimaryOffset,
		"shadowOffset", issue.ShadowOffset,
		"message", issue.Message,
		"err", issue.Err,
	)
}
