package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadutil"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type commandLogDrainLoadNativeStores struct {
	transactions CommandEventTransactionStore
	events       EventStore
}

type commandLogDrainLoadThreadTarget struct {
	boardID    string
	threadID   string
	rootPostID string
}

type commandLogDrainLoadPostProjection struct {
	id      string
	replyTo string
}

func (c *Core) RunCommandLogDrainLoad(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.CommandLogDrainLoadReport, error) {
	return c.runCommandLogDrainLoad(ctx, config, NewBrokerCommandLog(NewMemoryBrokerCommandLogClient()), commandLogDrainLoadNativeStores{})
}

func (c *Core) RunCommandLogDrainLoadWithCommandLog(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig, commandLog CommandLog) (loadmodel.CommandLogDrainLoadReport, error) {
	return c.runCommandLogDrainLoad(ctx, config, commandLog, commandLogDrainLoadNativeStores{})
}

func (c *Core) RunAuthoritativeCommandLogDrainLoad(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.CommandLogDrainLoadReport, error) {
	config.AuthoritativeSubmit = true
	if c == nil {
		return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil core")
	}
	return c.runCommandLogDrainLoad(ctx, config, c.commandLogAuthoritative, commandLogDrainLoadNativeStores{})
}

func (c *Core) RunNativeCommandEventProjectionLoad(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.CommandLogDrainLoadReport, error) {
	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	return c.RunNativeCommandEventProjectionLoadWithStores(
		ctx,
		config,
		NewBrokerCommandLog(commandClient),
		NewBrokerCommandEventTransactionStore(NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient)),
		NewBrokerEventStore(eventClient),
	)
}

func (c *Core) RunNativeCommandEventProjectionLoadWithStores(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig, commandLog CommandLog, transactions CommandEventTransactionStore, events EventStore) (loadmodel.CommandLogDrainLoadReport, error) {
	config.ExecutorMode = loadmodel.CommandLogDrainExecutorNative
	return c.runCommandLogDrainLoad(ctx, config, commandLog, commandLogDrainLoadNativeStores{
		transactions: transactions,
		events:       events,
	})
}

func (c *Core) runCommandLogDrainLoad(ctx context.Context, config loadmodel.CommandLogDrainLoadConfig, commandLog CommandLog, nativeStores commandLogDrainLoadNativeStores) (loadmodel.CommandLogDrainLoadReport, error) {
	if c == nil {
		return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil core")
	}
	if config.AuthoritativeSubmit {
		if c.commandLogAuthoritative == nil {
			return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: authoritative submit mode requires an authoritative command log")
		}
		commandLog = c.commandLogAuthoritative
	} else if c.commandLogAuthoritative != nil {
		return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: core must not already be in authoritative command-log mode")
	}
	if commandLog == nil {
		return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil command log")
	}
	config = loadmodel.NormalizeCommandLogDrainLoadConfig(config)
	if err := loadmodel.ValidateCommandLogDrainLoadConfig(config); err != nil {
		return loadmodel.CommandLogDrainLoadReport{}, err
	}
	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		if nativeStores.transactions == nil {
			return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: native executor requires command/event transactions")
		}
		if nativeStores.events == nil {
			return loadmodel.CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: native executor requires an event store")
		}
	}
	report := loadmodel.NewCommandLogDrainLoadReport(config, nowMS())

	actor, err := c.RegisterUser(config.UserName, newID("cl_"))
	if err != nil {
		return report, fmt.Errorf("register command-log load user: %w", err)
	}
	boardIDs, err := c.createCommandLogDrainLoadBoards(ctx, actor, config)
	if err != nil {
		return report, err
	}
	actorsByBoard, err := c.commandLogDrainLoadActorsByBoard(actor, boardIDs, config)
	if err != nil {
		return report, err
	}
	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		if seeder, ok := nativeStores.events.(interface {
			RequiresEventStoreProjectionWatermarkSeed() bool
		}); ok && seeder.RequiresEventStoreProjectionWatermarkSeed() {
			if _, err := c.seedEventStoreProjectionWatermarksFromEventPartitionOffsets(ctx, loadmodel.CommandLogDrainLoadEventProjectionSource(config)); err != nil {
				return commandLogDrainLoadReportError(report, err)
			}
		}
	}

	createConfig := config
	createConfig.RepliesPerThread = 0
	createCommands := loadmodel.CommandLogDrainLoadCreateThreadCommands(config)
	finalCommandPartitionLimit := loadmodel.CommandLogDrainLoadCommandPartitionLimit(config)

	if err := c.runCommandLogDrainLoadPhase(ctx, &report, commandLog, createConfig, nativeStores, createCommands, createConfig.Boards, func() (loadmodel.CommandLogLoadStage, error) {
		return c.produceCommandLogDrainLoad(ctx, commandLog, actorsByBoard, boardIDs, createConfig)
	}); err != nil {
		return commandLogDrainLoadReportError(report, err)
	}

	if config.RepliesPerThread > 0 {
		targets, err := c.commandLogDrainLoadThreadTargets(boardIDs, config)
		if err != nil {
			return commandLogDrainLoadReportError(report, err)
		}
		replyCommands := loadmodel.CommandLogDrainLoadAppendPostCommands(config)
		if err := c.runCommandLogDrainLoadPhase(ctx, &report, commandLog, config, nativeStores, replyCommands, finalCommandPartitionLimit, func() (loadmodel.CommandLogLoadStage, error) {
			return c.produceCommandLogAppendPostLoad(ctx, commandLog, actorsByBoard, targets, config)
		}); err != nil {
			return commandLogDrainLoadReportError(report, err)
		}
	}

	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		if err := c.validateCommandLogDrainLoadProjections(boardIDs, config); err != nil {
			return commandLogDrainLoadReportError(report, err)
		}
	}
	readiness, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{
		PartitionLimit: finalCommandPartitionLimit,
		BatchSize:      config.BatchSize,
	})
	if err != nil {
		return commandLogDrainLoadReportError(report, err)
	}
	loadmodel.FinalizeCommandLogDrainLoadReport(&report, readiness, nowMS())
	return report, loadmodel.ValidateCommandLogDrainLoadReport(report)
}

func commandLogDrainLoadReportError(report loadmodel.CommandLogDrainLoadReport, err error) (loadmodel.CommandLogDrainLoadReport, error) {
	loadmodel.FinishCommandLogDrainLoadReport(&report, nowMS())
	return report, err
}

func (c *Core) runCommandLogDrainLoadPhase(ctx context.Context, report *loadmodel.CommandLogDrainLoadReport, commandLog CommandLog, config loadmodel.CommandLogDrainLoadConfig, nativeStores commandLogDrainLoadNativeStores, expectedCommands, partitionLimit int, produce func() (loadmodel.CommandLogLoadStage, error)) error {
	if report == nil {
		return fmt.Errorf("command log drain load: nil report")
	}
	submit, err := produce()
	loadmodel.RecordCommandLogDrainLoadSubmit(report, submit)
	if err != nil {
		return err
	}
	lagBeforeDrain, err := maxCommandLogPartitionLag(ctx, commandLog)
	if err != nil {
		return err
	}
	loadmodel.RecordCommandLogDrainLoadBeforeDrainLag(report, lagBeforeDrain)
	drain, err := c.drainCommandLogLoad(ctx, commandLog, config, nativeStores, expectedCommands, partitionLimit)
	loadmodel.RecordCommandLogDrainLoadDrain(report, drain)
	if err != nil {
		return err
	}
	lagAfterDrain, err := maxCommandLogPartitionLag(ctx, commandLog)
	if err != nil {
		return err
	}
	loadmodel.RecordCommandLogDrainLoadAfterDrainLag(report, lagAfterDrain)
	if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
		eventProjection, err := c.projectCommandLogDrainLoadEvents(ctx, nativeStores.events, config)
		loadmodel.RecordCommandLogDrainLoadEventProjection(report, eventProjection)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) commandLogDrainLoadActorsByBoard(actor *projections.User, boardIDs []string, config loadmodel.CommandLogDrainLoadConfig) (map[string]*projections.User, error) {
	actorsByBoard := make(map[string]*projections.User, len(boardIDs))
	for _, boardID := range boardIDs {
		actorsByBoard[boardID] = actor
	}
	if actor == nil || len(boardIDs) <= 1 || config.ExecutorMode != loadmodel.CommandLogDrainExecutorNative || currentSQLFlavor != postgresFlavor {
		return actorsByBoard, nil
	}
	for i, boardID := range boardIDs {
		boardActor, err := c.RegisterUser(fmt.Sprintf("%s_%02d", config.UserName, i), newID("cl_"))
		if err != nil {
			return nil, fmt.Errorf("register command-log load actor for %s: %w", boardID, err)
		}
		actorsByBoard[boardID] = boardActor
	}
	return actorsByBoard, nil
}

func commandLogDrainLoadActorForBoard(actorsByBoard map[string]*projections.User, boardID string) (*projections.User, error) {
	actor := actorsByBoard[boardID]
	if actor == nil {
		return nil, fmt.Errorf("command log drain load: missing actor for board %s", boardID)
	}
	return actor, nil
}

func (c *Core) createCommandLogDrainLoadBoards(ctx context.Context, actor *projections.User, config loadmodel.CommandLogDrainLoadConfig) ([]string, error) {
	boardIDs := make([]string, 0, config.Boards)
	for i := 0; i < config.Boards; i++ {
		boardID := fmt.Sprintf("%s_%02d", loadutil.SafeID(config.BoardPrefix), i)
		payload, err := json.Marshal(proto.CreateBoardPayload{
			ID:          boardID,
			Name:        fmt.Sprintf("Load %02d", i),
			Description: "Command-log drain load fixture",
		})
		if err != nil {
			return nil, err
		}
		reply := c.handler.ExecutePartition(ctx, actor, proto.CmdCreateBoard, payload, fmt.Sprintf("cmdlog-load-board-%d", i), commandexec.Partition{
			Kind: partitionBoard,
			Key:  boardID,
		})
		if reply.Err != nil {
			return nil, fmt.Errorf("create command-log load board %s: %s (%s)", boardID, reply.Err.Message, reply.Err.Code)
		}
		boardIDs = append(boardIDs, boardID)
	}
	return boardIDs, nil
}

func (c *Core) produceCommandLogDrainLoad(ctx context.Context, commandLog CommandLog, actorsByBoard map[string]*projections.User, boardIDs []string, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.CommandLogLoadStage, error) {
	stage := loadmodel.CommandLogLoadStage{Commands: loadmodel.CommandLogDrainLoadCreateThreadCommands(config)}
	body := loadutil.Body(config.BodyBytes)
	return loadutil.RunCommandLogLoadSubmitStage(ctx, stage, config.SubmitConcurrency, "produce command-log load", func(workerID, i int) string {
		boardID := boardIDs[i%len(boardIDs)]
		actor, err := commandLogDrainLoadActorForBoard(actorsByBoard, boardID)
		if err != nil {
			return err.Error()
		}
		payload, err := json.Marshal(proto.CreateThreadPayload{
			Board: boardID,
			Title: fmt.Sprintf("command-log load %06d", i),
			Body:  body,
		})
		if err != nil {
			return err.Error()
		}
		cid := fmt.Sprintf("command-log-load-%d-%d", workerID, i)
		return c.submitCommandLogDrainLoadCommand(ctx, commandLog, actor, config, LogPartition{Kind: partitionBoard, Key: boardID}, proto.CmdCreateThread, payload, cid)
	})
}

func (c *Core) produceCommandLogAppendPostLoad(ctx context.Context, commandLog CommandLog, actorsByBoard map[string]*projections.User, targets []commandLogDrainLoadThreadTarget, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.CommandLogLoadStage, error) {
	stage := loadmodel.CommandLogLoadStage{Commands: len(targets) * config.RepliesPerThread}
	body := loadutil.Body(config.BodyBytes)
	return loadutil.RunCommandLogLoadSubmitStage(ctx, stage, config.SubmitConcurrency, "produce command-log appendPost load", func(workerID, i int) string {
		target := targets[i/config.RepliesPerThread]
		actor, err := commandLogDrainLoadActorForBoard(actorsByBoard, target.boardID)
		if err != nil {
			return err.Error()
		}
		replyIndex := i % config.RepliesPerThread
		payload := proto.AppendPostPayload{
			Thread: target.threadID,
			Body:   fmt.Sprintf("command-log reply %06d\n%s", i, body),
		}
		if config.DirectedReplies {
			payload.ReplyTo = target.rootPostID
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err.Error()
		}
		cid := fmt.Sprintf("command-log-reply-load-%d-%d-%d", workerID, i, replyIndex)
		return c.submitCommandLogDrainLoadCommand(ctx, commandLog, actor, config, LogPartition{Kind: partitionThread, Key: target.threadID}, proto.CmdAppendPost, raw, cid)
	})
}

func (c *Core) submitCommandLogDrainLoadCommand(ctx context.Context, commandLog CommandLog, actor *projections.User, config loadmodel.CommandLogDrainLoadConfig, partition LogPartition, command proto.CommandName, payload json.RawMessage, cid string) string {
	if config.AuthoritativeSubmit {
		reply := c.ExecCmd(ctx, actor, command, payload, cid)
		if reply.Err != nil {
			return reply.Err.Message
		}
		if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset <= 0 {
			return "authoritative submit did not return a pending command-log receipt"
		}
		return ""
	}
	_, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    actor.ID,
		CID:        cid,
		Command:    command,
		Payload:    payload,
		EnqueuedAt: nowMS(),
	})
	if err != nil {
		return err.Error()
	}
	return ""
}

func (c *Core) drainCommandLogLoad(ctx context.Context, commandLog CommandLog, config loadmodel.CommandLogDrainLoadConfig, nativeStores commandLogDrainLoadNativeStores, expectedCommands, partitionLimit int) (loadmodel.CommandLogDrainStage, error) {
	stage := loadmodel.CommandLogDrainStage{Commands: expectedCommands}
	if stage.Commands <= 0 {
		return stage, nil
	}
	members := make([]string, 0, config.Writers)
	for i := 0; i < config.Writers; i++ {
		members = append(members, fmt.Sprintf("writer-%02d", i))
	}
	assigner, err := newCommandLogDrainLoadAssigner(ctx, commandLog, members, config, partitionLimit)
	if err != nil {
		return stage, err
	}
	start := time.Now()
	noProgressRounds := 0
	errSamples := map[string]bool{}
	for {
		lag, err := maxCommandLogPartitionLag(ctx, commandLog)
		if err != nil {
			return stage, err
		}
		if lag == 0 {
			break
		}
		stage.Rounds++
		beforeProcessed := stage.Processed
		results := make(chan commandLogLoadDrainResult, len(members))
		var wg sync.WaitGroup
		for _, member := range members {
			member := member
			wg.Add(1)
			go func() {
				defer wg.Done()
				if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
					if batchStore, ok := nativeStores.transactions.(CommandEventTransactionBatchStore); ok {
						nativeExecutor := NewCommandLogNativeDecisionExecutor(c)
						drainResults, err := c.drainCommandLogLoadNativeBatchMember(ctx, commandLog, assigner, member, config, batchStore, nativeExecutor, partitionLimit)
						results <- commandLogLoadDrainResult{results: drainResults, err: err}
						return
					}
				}
				executor := CommandLogExecutor(c)
				var finalizer CommandLogFinalizer
				if config.ExecutorMode == loadmodel.CommandLogDrainExecutorNative {
					nativeExecutor := NewCommandLogNativeDecisionExecutor(c)
					executor = nativeExecutor
					finalizer = CommandEventTransactionBatchFinalizer{
						CommandEventTransactionFinalizer: CommandEventTransactionFinalizer{
							Transactions:      nativeStores.transactions,
							Events:            nativeExecutor,
							Applied:           c,
							TerminalFailures:  c,
							RetryableFailures: c,
						},
					}
				}
				worker := NewCommandLogWorker(CommandLogWorkerConfig{
					Log:                  commandLog,
					Assignments:          assigner,
					Executor:             executor,
					Finalizer:            finalizer,
					OwnerID:              member,
					BatchSize:            config.BatchSize,
					PartitionLimit:       partitionLimit,
					PartitionConcurrency: config.PartitionConcurrency,
				})
				drainResults, err := worker.DrainOnce(ctx)
				results <- commandLogLoadDrainResult{results: drainResults, err: err}
			}()
		}
		wg.Wait()
		close(results)
		for workerResult := range results {
			accumulateCommandLogDrainWorkerResults(&stage, errSamples, workerResult.results)
			if workerResult.err != nil {
				loadmodel.AddCommandLogDrainSample(&stage, errSamples, workerResult.err.Error())
				return stage, workerResult.err
			}
		}
		if stage.Processed == beforeProcessed {
			noProgressRounds++
			if noProgressRounds >= 3 {
				err := fmt.Errorf("command log drain load: no worker progress after %d rounds with lag %d", noProgressRounds, lag)
				loadmodel.AddCommandLogDrainSample(&stage, errSamples, err.Error())
				return stage, err
			}
		} else {
			noProgressRounds = 0
		}
	}
	elapsed := time.Since(start)
	stage.DurationMS = elapsed.Milliseconds()
	stage.CommandsPerSec = loadutil.PerSecond(stage.Applied, elapsed)
	return stage, nil
}

type commandLogDrainLoadNativePendingRecord struct {
	record     CommandLogRecord
	reply      commandexec.Reply
	eventStart int
	eventEnd   int
}

type commandLogDrainLoadNativePendingPartition struct {
	result  CommandLogWorkerResult
	records []commandLogDrainLoadNativePendingRecord
	events  []EventAppend
}

func (p *commandLogDrainLoadNativePendingPartition) appendRecord(ctx context.Context, executor *CommandLogNativeDecisionExecutor, record CommandLogRecord, reply commandexec.Reply) error {
	if p == nil {
		return fmt.Errorf("command log drain load: nil pending partition")
	}
	eventStart := len(p.events)
	if reply.Err == nil {
		events, err := executor.DecideCommandLogEvents(ctx, record, reply)
		if err != nil {
			return err
		}
		p.events = append(p.events, events...)
	}
	eventEnd := len(p.events)
	p.records = append(p.records, commandLogDrainLoadNativePendingRecord{
		record:     record,
		reply:      reply,
		eventStart: eventStart,
		eventEnd:   eventEnd,
	})
	return nil
}

type commandLogDrainLoadMemberPartitionCursor struct {
	partition       LogPartition
	committedOffset int64
}

func (c *Core) drainCommandLogLoadNativeBatchMember(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, config loadmodel.CommandLogDrainLoadConfig, transactions CommandEventTransactionBatchStore, executor *CommandLogNativeDecisionExecutor, partitionLimit int) ([]CommandLogWorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("command log drain load: nil core")
	}
	if commandLog == nil {
		return nil, fmt.Errorf("command log drain load: nil command log")
	}
	if transactions == nil {
		return nil, fmt.Errorf("command log drain load: nil batch transaction store")
	}
	if executor == nil {
		return nil, fmt.Errorf("command log drain load: nil native executor")
	}
	cursors, err := commandLogDrainLoadMemberPartitionCursors(ctx, commandLog, assigner, ownerID, partitionLimit)
	if err != nil {
		return nil, err
	}
	results := make([]CommandLogWorkerResult, 0, len(cursors))
	pending := make([]commandLogDrainLoadNativePendingPartition, 0, len(cursors))

	for _, cursor := range cursors {
		partition := cursor.partition.Normalize()
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			return results, err
		}
		if !assigned {
			results = append(results, commandLogWorkerAssignmentResult(partition, assignment, false))
			continue
		}
		committed := cursor.committedOffset
		result := commandLogWorkerClaimedAssignmentResult(partition, assignment, ownerID, committed)
		records, err := commandLog.FetchPartition(ctx, partition, committed, config.BatchSize)
		allowCommandLogRebalance(commandLog)
		if err != nil {
			results = append(results, result)
			return results, err
		}
		pendingPartition := commandLogDrainLoadNativePendingPartition{result: result}
		pendingLastOffset := committed
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				results = append(results, pendingPartition.result)
				return results, err
			}
			if record.Offset <= pendingLastOffset {
				continue
			}
			if err := validateCommandLogWorkerOffsetProgress(partition, pendingLastOffset, record); err != nil {
				results = append(results, pendingPartition.result)
				return results, err
			}
			reply := executor.ExecuteCommandLogRecord(ctx, record)
			if reply.Err != nil && reply.Err.Retryable {
				results, pending = commandLogDrainLoadQueuePendingPartition(results, pending, pendingPartition)
				if err := c.flushCommandLogDrainLoadNativePendingResults(ctx, commandLog, transactions, &results, pending); err != nil {
					return results, err
				}
				pending = nil
				retryResult := commandLogWorkerClaimedAssignmentResult(partition, assignment, ownerID, committed)
				retryResult.LastOffset = pendingLastOffset
				retryResult.RetryableFailure = reply.Err
				if err := c.RecordCommandLogRetryableFailure(ctx, record, reply.Err); err != nil {
					retryResult.FinalizerFailure = err.Error()
					results = append(results, retryResult)
					return results, err
				}
				results = append(results, retryResult)
				return results, nil
			}
			if err := pendingPartition.appendRecord(ctx, executor, record, reply); err != nil {
				results = append(results, pendingPartition.result)
				return results, err
			}
			pendingLastOffset = record.Offset
		}
		results, pending = commandLogDrainLoadQueuePendingPartition(results, pending, pendingPartition)
	}
	if err := c.flushCommandLogDrainLoadNativePendingResults(ctx, commandLog, transactions, &results, pending); err != nil {
		return results, err
	}
	return results, nil
}

func (c *Core) flushCommandLogDrainLoadNativePendingResults(ctx context.Context, commandLog CommandLog, transactions CommandEventTransactionBatchStore, results *[]CommandLogWorkerResult, pending []commandLogDrainLoadNativePendingPartition) error {
	err := c.flushCommandLogDrainLoadNativePending(ctx, commandLog, transactions, pending)
	for _, partition := range pending {
		*results = append(*results, partition.result)
	}
	return err
}

func (c *Core) flushCommandLogDrainLoadNativePending(ctx context.Context, commandLog CommandLog, transactions CommandEventTransactionBatchStore, pending []commandLogDrainLoadNativePendingPartition) error {
	if len(pending) == 0 {
		return nil
	}
	txs := make([]logmodel.CommandEventTransaction, 0, len(pending))
	for _, partition := range pending {
		if len(partition.records) == 0 {
			continue
		}
		last := partition.records[len(partition.records)-1].record
		txs = append(txs, logmodel.CommandEventTransaction{
			CommandPartition:      last.Partition,
			CommandOffset:         last.Offset,
			CommandSourcePosition: last.SourcePosition,
			Events:                partition.events,
		})
	}
	committed, err := transactions.CommitCommandEventBatch(ctx, txs)
	if err != nil {
		commandLogDrainLoadMarkPendingCommitFailure(pending, err)
		return err
	}
	if len(committed) != len(pending) {
		err := fmt.Errorf("command log drain load: batch committed %d partitions for %d pending partitions", len(committed), len(pending))
		commandLogDrainLoadMarkPendingCommitFailure(pending, err)
		return err
	}
	appliedRecords := []CommandLogRecord{}
	appliedResults := []*proto.AckResult{}
	for i := range pending {
		partition := &pending[i]
		result := &partition.result
		committedResult := committed[i]
		committedPartition := committedResult.CommittedPartition.Normalize()
		resultPartition := result.Partition.Normalize()
		if committedPartition != resultPartition {
			err := fmt.Errorf("command log drain load: committed partition %s/%s for pending partition %s/%s",
				committedPartition.Kind, committedPartition.Key,
				resultPartition.Kind, resultPartition.Key)
			result.CommitFailures++
			result.CommitFailure = err.Error()
			return err
		}
		for _, pendingRecord := range partition.records {
			if pendingRecord.reply.Err != nil {
				result.TerminalFailures++
				result.TerminalFailure = pendingRecord.reply.Err
				if err := c.RecordCommandLogTerminalFailure(ctx, pendingRecord.record, pendingRecord.reply.Err); err != nil {
					result.FinalizerFailure = err.Error()
					return err
				}
				continue
			}
			result.Applied++
			if pendingRecord.reply.Result != nil {
				if pendingRecord.reply.Result.Seq <= 0 {
					for _, evt := range committedResult.Events[pendingRecord.eventStart:pendingRecord.eventEnd] {
						if evt != nil && evt.Seq > pendingRecord.reply.Result.Seq {
							pendingRecord.reply.Result.Seq = evt.Seq
						}
					}
				}
				appliedRecords = append(appliedRecords, pendingRecord.record)
				appliedResults = append(appliedResults, pendingRecord.reply.Result)
			}
		}
		last := partition.records[len(partition.records)-1].record
		if committedResult.CommittedOffset < last.Offset {
			err := fmt.Errorf("command log drain load: committed offset %d before pending offset %d for %s/%s",
				committedResult.CommittedOffset, last.Offset, result.Partition.Kind, result.Partition.Key)
			result.CommitFailures++
			result.CommitFailure = err.Error()
			return err
		}
		if err := recordCommandLogDrainLoadCommit(ctx, commandLog, result.Partition, last.Offset, result); err != nil {
			return err
		}
		advanceCommandLogWorkerResult(result, last.Offset, len(partition.records))
	}
	if len(appliedRecords) > 0 {
		if err := c.RecordCommandLogAppliedBatch(ctx, appliedRecords, appliedResults); err != nil {
			for i := range pending {
				for _, pendingRecord := range pending[i].records {
					if pendingRecord.reply.Err == nil && pendingRecord.reply.Result != nil {
						pending[i].result.FinalizerFailure = err.Error()
						break
					}
				}
			}
			return err
		}
	}
	return nil
}

func commandLogDrainLoadQueuePendingPartition(results []CommandLogWorkerResult, pending []commandLogDrainLoadNativePendingPartition, partition commandLogDrainLoadNativePendingPartition) ([]CommandLogWorkerResult, []commandLogDrainLoadNativePendingPartition) {
	if len(partition.records) > 0 {
		return results, append(pending, partition)
	}
	return append(results, partition.result), pending
}

func commandLogDrainLoadMarkPendingCommitFailure(pending []commandLogDrainLoadNativePendingPartition, err error) {
	if err == nil {
		return
	}
	for i := range pending {
		pending[i].result.CommitFailures++
		pending[i].result.CommitFailure = err.Error()
	}
}

func commandLogDrainLoadMemberPartitionCursors(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, partitionLimit int) ([]commandLogDrainLoadMemberPartitionCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offsetLister, ok := commandLog.(CommandPartitionOffsetLister); ok {
		offsets, _, err := listCommandPartitionOffsetsWithLimit(ctx, offsetLister, partitionLimit)
		if err != nil {
			return nil, err
		}
		assignedOffsets, err := commandLogDrainLoadAssignedLaggingPartitionOffsets(ctx, offsets, assigner, ownerID)
		if err != nil {
			return nil, err
		}
		cursors := make([]commandLogDrainLoadMemberPartitionCursor, 0, len(assignedOffsets))
		for _, offset := range assignedOffsets {
			cursors = append(cursors, commandLogDrainLoadMemberPartitionCursor{
				partition:       offset.Partition,
				committedOffset: offset.CommittedOffset,
			})
		}
		return cursors, nil
	}
	partitions, err := commandLogDrainLoadMemberPartitions(ctx, commandLog, assigner, ownerID, partitionLimit)
	if err != nil {
		return nil, err
	}
	cursors := make([]commandLogDrainLoadMemberPartitionCursor, 0, len(partitions))
	for _, partition := range partitions {
		partition = partition.Normalize()
		committed, err := commandLog.CommittedOffset(ctx, partition)
		if err != nil {
			return nil, err
		}
		cursors = append(cursors, commandLogDrainLoadMemberPartitionCursor{
			partition:       partition,
			committedOffset: committed,
		})
	}
	return cursors, nil
}

func commandLogDrainLoadAssignedLaggingPartitionOffsets(ctx context.Context, offsets []logmodel.CommandPartitionOffset, assigner CommandPartitionAssigner, ownerID string) ([]logmodel.CommandPartitionOffset, error) {
	ownerID = strings.TrimSpace(ownerID)
	assignedOffsets := make([]logmodel.CommandPartitionOffset, 0, len(offsets))
	for _, offset := range logmodel.LaggingCommandPartitionOffsets(offsets) {
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, offset.Partition)
		if err != nil {
			return nil, err
		}
		if !assigned || assignment.OwnerID != ownerID {
			continue
		}
		assignedOffsets = append(assignedOffsets, offset)
	}
	return assignedOffsets, nil
}

func commandLogDrainLoadMemberPartitions(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, partitionLimit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lister, ok := assigner.(CommandPartitionAssignmentLister); ok {
		assignments, err := lister.ListAssignedCommandPartitions(ctx, ownerID, partitionLimit)
		if err != nil {
			return nil, err
		}
		return logmodel.CommandPartitionAssignmentPartitions(assignments), nil
	}
	lister, ok := commandLog.(CommandPartitionLister)
	if !ok {
		return nil, fmt.Errorf("command log drain load: command log does not expose partitions")
	}
	partitions, err := lister.ListCommandPartitions(ctx, partitionLimit)
	if err != nil {
		return nil, err
	}
	out := make([]LogPartition, 0, len(partitions))
	for _, partition := range partitions {
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			return nil, err
		}
		if assigned && assignment.OwnerID == ownerID {
			out = append(out, partition.Normalize())
		}
	}
	return out, nil
}

func recordCommandLogDrainLoadCommit(ctx context.Context, commandLog CommandLog, partition LogPartition, offset int64, result *CommandLogWorkerResult) error {
	recorder, ok := commandLog.(CommandLogCommitRecorder)
	if !ok {
		return nil
	}
	if err := recorder.RecordCommandLogCommit(ctx, partition, offset); err != nil {
		if result != nil {
			result.CommitFailures++
			result.CommitFailure = err.Error()
		}
		return err
	}
	if result != nil {
		result.CommitFailure = ""
	}
	return nil
}

func newCommandLogDrainLoadAssigner(ctx context.Context, commandLog CommandLog, members []string, config loadmodel.CommandLogDrainLoadConfig, partitionLimit int) (CommandPartitionAssigner, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("command log drain load: assignment requires at least one writer")
	}
	switch loadmodel.NormalizeCommandLogDrainAssignmentMode(config.AssignmentMode) {
	case loadmodel.CommandLogDrainAssignmentHash:
		return logmodel.NewHashCommandPartitionAssigner(members, 1), nil
	case loadmodel.CommandLogDrainAssignmentSnapshot:
		lister, err := requireCommandPartitionOffsetLister(commandLog, "command log drain load: snapshot assignment requires command partition offsets")
		if err != nil {
			return nil, err
		}
		snapshot, err := snapshotCommandLogDrainLoadPartitionOffsets(ctx, lister, partitionLimit)
		if err != nil {
			return nil, err
		}
		return commandLogDrainLoadSnapshotAssigner{
			SnapshotCommandPartitionAssigner: logmodel.NewSnapshotCommandPartitionAssigner(logmodel.CommandPartitionAssignmentSnapshotForLaggingOffsets(snapshot, members, 1)),
			offsets:                          snapshot,
			partitionLimit:                   partitionLimit,
		}, nil
	default:
		return nil, fmt.Errorf("command log drain load: unsupported assignment mode %q", config.AssignmentMode)
	}
}

func snapshotCommandLogDrainLoadPartitionOffsets(ctx context.Context, lister CommandPartitionOffsetLister, limit int) (logmodel.CommandPartitionOffsetSnapshot, error) {
	if lister == nil {
		return nil, fmt.Errorf("command log drain load: nil partition offsets")
	}
	offsets, _, err := listCommandPartitionOffsetsWithLimit(ctx, lister, limit)
	if err != nil {
		return nil, err
	}
	return logmodel.NewCommandPartitionOffsetSnapshot(offsets, limit), nil
}

type commandLogDrainLoadSnapshotAssigner struct {
	*logmodel.SnapshotCommandPartitionAssigner
	offsets        CommandPartitionOffsetLister
	partitionLimit int
}

func (a commandLogDrainLoadSnapshotAssigner) StableCommandPartitionAssignment() bool {
	return true
}

func (a commandLogDrainLoadSnapshotAssigner) ListAssignedCommandPartitionOffsets(ctx context.Context, ownerID string, limit int) ([]logmodel.CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.offsets == nil {
		return nil, fmt.Errorf("command log drain load: nil partition offsets")
	}
	queryLimit := a.partitionLimit
	if limit > 0 && (queryLimit <= 0 || limit < queryLimit) {
		queryLimit = limit
	}
	offsets, _, err := listCommandPartitionOffsetsWithLimit(ctx, a.offsets, queryLimit)
	if err != nil {
		return nil, err
	}
	return commandLogDrainLoadAssignedLaggingPartitionOffsets(ctx, offsets, a, ownerID)
}

func (c *Core) projectCommandLogDrainLoadEvents(ctx context.Context, eventStore EventStore, config loadmodel.CommandLogDrainLoadConfig) (loadmodel.EventStoreProjectionLoadStage, error) {
	partitionLimit := loadmodel.CommandLogDrainLoadCommandPartitionLimit(config)
	stage := loadmodel.EventStoreProjectionLoadStage{
		Enabled:        true,
		PartitionLimit: partitionLimit,
	}
	lister, ok := eventStore.(EventPartitionLister)
	if !ok {
		err := fmt.Errorf("command log drain load: event store does not expose partitions")
		return commandLogDrainLoadProjectionError(stage, time.Time{}, err)
	}
	partitions, limited, err := listEventPartitionsWithLimit(ctx, lister, partitionLimit)
	if err != nil {
		return commandLogDrainLoadProjectionError(stage, time.Time{}, err)
	}
	stage.Partitions = len(partitions)
	stage.PartitionLimitExceeded = limited
	if limited {
		err := fmt.Errorf("command log drain load: event projection partition limit %d did not cover every broker event partition", partitionLimit)
		return commandLogDrainLoadProjectionError(stage, time.Time{}, err)
	}
	targetOffsets, hasTargetOffsets, err := eventStoreProjectionTargetOffsets(ctx, eventStore, partitions, partitionLimit)
	if err != nil {
		return commandLogDrainLoadProjectionError(stage, time.Time{}, err)
	}
	start := time.Now()
	seenPartitions := map[LogPartition]bool{}
	projectionSource := loadmodel.CommandLogDrainLoadEventProjectionSource(config)
	projectionConcurrency := 1
	if currentSQLFlavor == postgresFlavor {
		projectionConcurrency = config.Writers
	}
	for {
		if err := ctx.Err(); err != nil {
			return commandLogDrainLoadProjectionError(stage, start, err)
		}
		stage.Rounds++
		results, err := c.materializeCommandLogDrainLoadEventPartitions(ctx, eventStore, partitions, projectionSource, config.BatchSize, projectionConcurrency)
		if err != nil {
			return commandLogDrainLoadProjectionError(stage, start, err)
		}
		for _, result := range results {
			seenPartitions[result.Partition.Normalize()] = true
			stage.AppliedEvents += result.Applied
		}
		if hasTargetOffsets {
			if commandLogDrainLoadEventProjectionTargetsReached(results, targetOffsets) {
				break
			}
			continue
		}
		if !eventStoreProjectionWorkerShouldContinue(results, config.BatchSize) {
			break
		}
	}
	stage.DurationMS = time.Since(start).Milliseconds()
	stage.Partitions = len(seenPartitions)
	stage.EventsPerSec = loadutil.PerSecond(stage.AppliedEvents, time.Duration(stage.DurationMS)*time.Millisecond)
	return stage, nil
}

func commandLogDrainLoadProjectionError(stage loadmodel.EventStoreProjectionLoadStage, start time.Time, err error) (loadmodel.EventStoreProjectionLoadStage, error) {
	if !start.IsZero() {
		stage.DurationMS = time.Since(start).Milliseconds()
	}
	if err != nil {
		loadmodel.AddLoadSample(&stage.SampleErrorText, nil, err.Error())
	}
	return stage, err
}

func (c *Core) materializeCommandLogDrainLoadEventPartitions(ctx context.Context, eventStore EventStore, partitions []LogPartition, source string, batchSize, concurrency int) ([]EventStorePartitionMaterializationResult, error) {
	results := make([]EventStorePartitionMaterializationResult, len(partitions))
	if len(partitions) == 0 {
		return results, nil
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(partitions) {
		concurrency = len(partitions)
	}
	type job struct {
		index     int
		partition LogPartition
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan job)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := workCtx.Err(); err != nil {
					return
				}
				result, err := c.MaterializeEventStorePartition(workCtx, eventStore, EventStorePartitionMaterializationConfig{
					Source:    source,
					Partition: job.partition,
					Limit:     batchSize,
				})
				results[job.index] = result
				if err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
			}
		}()
	}
dispatch:
	for i, partition := range partitions {
		select {
		case <-workCtx.Done():
			break dispatch
		case jobs <- job{index: i, partition: partition}:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return results, firstErr
	}
	if err := workCtx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

func commandLogDrainLoadEventProjectionTargetsReached(results []EventStorePartitionMaterializationResult, targets map[LogPartition]int64) bool {
	offsets := make(map[LogPartition]int64, len(results))
	for _, result := range results {
		offsets[result.Partition.Normalize()] = result.LastOffset
	}
	return logmodel.PartitionOffsetsReached(offsets, targets)
}

func eventStoreProjectionTargetOffsets(ctx context.Context, eventStore EventStore, partitions []LogPartition, limit int) (map[LogPartition]int64, bool, error) {
	lister, ok := eventStore.(EventPartitionOffsetLister)
	if !ok {
		return nil, false, nil
	}
	offsets, limited, err := listEventPartitionOffsets(ctx, lister, limit)
	if err != nil {
		return nil, false, err
	}
	if limited {
		return nil, false, fmt.Errorf("event projection partition offset limit %d did not cover every broker event partition", limit)
	}
	return logmodel.EventProjectionTargetOffsets(partitions, offsets), true, nil
}

func (c *Core) commandLogDrainLoadThreadTargets(boardIDs []string, config loadmodel.CommandLogDrainLoadConfig) ([]commandLogDrainLoadThreadTarget, error) {
	if len(boardIDs) == 0 {
		return nil, nil
	}
	rows, err := qQuery(c.DB,
		`SELECT t.board, t.id,
		        COALESCE((
		          SELECT p.id
		            FROM posts p
		           WHERE p.thread=t.id
		           ORDER BY p.created_seq
		           LIMIT 1
		        ), '') AS root_post_id
		   FROM threads t
		  WHERE t.board IN (`+queryPlaceholders(len(boardIDs))+`)
		  ORDER BY t.board, t.last_seq DESC, t.id`,
		stringQueryArgs(boardIDs)...,
	)
	if err != nil {
		return nil, fmt.Errorf("command log drain load: list projected reply targets: %w", err)
	}
	defer rows.Close()

	targets := make([]commandLogDrainLoadThreadTarget, 0, loadmodel.CommandLogDrainLoadCreateThreadCommands(config))
	counts := make(map[string]int, len(boardIDs))
	for rows.Next() {
		var boardID, threadID, rootPostID string
		if err := rows.Scan(&boardID, &threadID, &rootPostID); err != nil {
			return nil, err
		}
		counts[boardID]++
		if rootPostID == "" {
			return nil, fmt.Errorf("command log drain load: thread %s projected 0 root posts, want 1", threadID)
		}
		targets = append(targets, commandLogDrainLoadThreadTarget{
			boardID:    boardID,
			threadID:   threadID,
			rootPostID: rootPostID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, boardID := range boardIDs {
		if counts[boardID] != config.CommandsPerBoard {
			return nil, fmt.Errorf("command log drain load: board %s projected %d reply targets, want %d", boardID, counts[boardID], config.CommandsPerBoard)
		}
	}
	return targets, nil
}

func (c *Core) commandLogDrainLoadPostsByThread(threadIDs []string) (map[string][]commandLogDrainLoadPostProjection, error) {
	postsByThread := make(map[string][]commandLogDrainLoadPostProjection, len(threadIDs))
	const chunkSize = 500
	for start := 0; start < len(threadIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(threadIDs) {
			end = len(threadIDs)
		}
		chunk := threadIDs[start:end]
		rows, err := qQuery(c.DB,
			`SELECT thread, id, COALESCE(reply_to, '')
			   FROM posts
			  WHERE thread IN (`+queryPlaceholders(len(chunk))+`)
			  ORDER BY thread, created_seq, id`,
			stringQueryArgs(chunk)...,
		)
		if err != nil {
			return nil, fmt.Errorf("command log drain load: list projected posts: %w", err)
		}
		for rows.Next() {
			var threadID, postID, replyTo string
			if err := rows.Scan(&threadID, &postID, &replyTo); err != nil {
				rows.Close()
				return nil, err
			}
			postsByThread[threadID] = append(postsByThread[threadID], commandLogDrainLoadPostProjection{
				id:      postID,
				replyTo: replyTo,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return postsByThread, nil
}

func (c *Core) validateCommandLogDrainLoadProjections(boardIDs []string, config loadmodel.CommandLogDrainLoadConfig) error {
	targets, err := c.commandLogDrainLoadThreadTargets(boardIDs, config)
	if err != nil {
		return err
	}
	threadIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		threadIDs = append(threadIDs, target.threadID)
	}
	postsByThread, err := c.commandLogDrainLoadPostsByThread(threadIDs)
	if err != nil {
		return err
	}
	wantPosts := 1 + config.RepliesPerThread
	for _, target := range targets {
		posts := postsByThread[target.threadID]
		if len(posts) != wantPosts {
			return fmt.Errorf("command log drain load: thread %s projected %d posts, want %d", target.threadID, len(posts), wantPosts)
		}
		if config.DirectedReplies && config.RepliesPerThread > 0 {
			rootPostID := posts[0].id
			for _, post := range posts[1:] {
				if post.replyTo != rootPostID {
					return fmt.Errorf("command log drain load: thread %s reply %s points to %s, want root %s",
						target.threadID, post.id, post.replyTo, rootPostID)
				}
			}
		}
	}
	return nil
}

type commandLogLoadDrainResult struct {
	results []CommandLogWorkerResult
	err     error
}

func accumulateCommandLogDrainWorkerResults(stage *loadmodel.CommandLogDrainStage, errSamples map[string]bool, results []CommandLogWorkerResult) {
	if stage == nil {
		return
	}
	for _, result := range results {
		stage.Processed += result.Processed
		stage.Applied += result.Applied
		stage.TerminalFailures += result.TerminalFailures
		stage.CommitFailures += result.CommitFailures
		if result.TerminalFailure != nil {
			loadmodel.AddCommandLogDrainPartitionSample(stage, errSamples, result.Partition.Kind, result.Partition.Key, "terminal", commandLogDrainFailureMessage(result.TerminalFailure))
		}
		if result.RetryableFailure != nil {
			stage.RetryableFailures++
			loadmodel.AddCommandLogDrainPartitionSample(stage, errSamples, result.Partition.Kind, result.Partition.Key, "retryable", commandLogDrainFailureMessage(result.RetryableFailure))
		}
		if result.AssignmentLost {
			stage.AssignmentLosses++
			loadmodel.AddCommandLogDrainPartitionSample(stage, errSamples, result.Partition.Kind, result.Partition.Key, "assignment lost", "")
		}
		if result.ClaimLost {
			stage.ClaimLosses++
			loadmodel.AddCommandLogDrainPartitionSample(stage, errSamples, result.Partition.Kind, result.Partition.Key, "claim lost", "")
		}
		if result.CommitFailure != "" {
			loadmodel.AddCommandLogDrainSample(stage, errSamples, result.CommitFailure)
		}
		if result.FinalizerFailure != "" {
			loadmodel.AddCommandLogDrainPartitionSample(stage, errSamples, result.Partition.Kind, result.Partition.Key, "finalizer", result.FinalizerFailure)
		}
	}
}

func commandLogDrainFailureMessage(errDetail *proto.ErrorDetail) string {
	if errDetail == nil {
		return ""
	}
	if message := strings.TrimSpace(errDetail.Message); message != "" {
		return message
	}
	return strings.TrimSpace(errDetail.Code)
}

func maxCommandLogPartitionLag(ctx context.Context, commandLog CommandLog) (int64, error) {
	offsets, _, err := listCommandLogPartitionOffsetsWithLimit(ctx, commandLog, 0, "command log drain load: command log does not expose partition offsets")
	if err != nil {
		return 0, err
	}
	var maxLag int64
	for _, offset := range offsets {
		lag := offset.Lag()
		if lag > maxLag {
			maxLag = lag
		}
	}
	return maxLag, nil
}
