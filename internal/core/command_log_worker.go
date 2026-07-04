package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// CommandLogExecutor is the writer/decider boundary for broker-owned command
// partitions. The current bridge can execute through the SQL-backed handler;
// later implementations can decide events without touching SQL projections.
type CommandLogExecutor interface {
	ExecuteCommandLogRecord(ctx context.Context, record CommandLogRecord) Reply
}

type CommandLogExecutorFunc func(context.Context, CommandLogRecord) Reply

func (f CommandLogExecutorFunc) ExecuteCommandLogRecord(ctx context.Context, record CommandLogRecord) Reply {
	return f(ctx, record)
}

type CommandLogTerminalFailureRecorder interface {
	RecordCommandLogTerminalFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error
}

type CommandLogRetryableFailureRecorder interface {
	RecordCommandLogRetryableFailure(ctx context.Context, record CommandLogRecord, errDetail *proto.ErrorDetail) error
}

type CommandLogAppliedRecorder interface {
	RecordCommandLogApplied(ctx context.Context, record CommandLogRecord, result *proto.AckResult) error
}

type CommandLogBatchAppliedRecorder interface {
	RecordCommandLogAppliedBatch(ctx context.Context, records []CommandLogRecord, results []*proto.AckResult) error
}

// CommandLogFinalizer owns the post-decision bridge from one command-log record
// to visible outcome plus command-offset advancement. The default implementation
// records SQL-backed command receipts while the worker performs the guarded
// offset commit; future broker-native writers can replace it with an event-log
// append/offset commit transaction and return Committed.
type CommandLogFinalizer interface {
	FinalizeCommandLogRecord(ctx context.Context, record CommandLogRecord, reply Reply) (CommandLogFinalizationResult, error)
}

type CommandLogCommitRecorder interface {
	RecordCommandLogCommit(ctx context.Context, partition LogPartition, offset int64) error
}

// CommandLogBatchFinalizer can atomically finalize a contiguous batch from one
// command partition. Implementations should return Committed only after the
// batch's last command offset is durable.
type CommandLogBatchFinalizer interface {
	FinalizeCommandLogBatch(ctx context.Context, records []CommandLogRecord, replies []Reply) (CommandLogFinalizationResult, error)
}

type CommandLogFinalizationResult struct {
	Applied          int
	TerminalFailures int
	TerminalFailure  *proto.ErrorDetail
	RetryableFailure *proto.ErrorDetail
	CommitFailures   int
	CommitFailure    string
	Committed        bool
}

type CommandLogFinalizerFunc func(context.Context, CommandLogRecord, Reply) (CommandLogFinalizationResult, error)

func (f CommandLogFinalizerFunc) FinalizeCommandLogRecord(ctx context.Context, record CommandLogRecord, reply Reply) (CommandLogFinalizationResult, error) {
	return f(ctx, record, reply)
}

type CommandLogWorkerConfig struct {
	Log                  CommandLog
	Partitions           CommandPartitionLister
	Assignments          CommandPartitionAssigner
	Claims               CommandPartitionClaimer
	Executor             CommandLogExecutor
	Finalizer            CommandLogFinalizer
	Applied              CommandLogAppliedRecorder
	TerminalFailures     CommandLogTerminalFailureRecorder
	RetryableFailures    CommandLogRetryableFailureRecorder
	OwnerID              string
	BatchSize            int
	PartitionLimit       int
	PartitionConcurrency int
	CommitAttempts       int
	Interval             time.Duration
	ClaimTTL             time.Duration
	ClaimRefreshInterval time.Duration
}

type CommandLogWorker struct {
	log                  CommandLog
	partitions           CommandPartitionLister
	assignments          CommandPartitionAssigner
	claims               CommandPartitionClaimer
	executor             CommandLogExecutor
	finalizer            CommandLogFinalizer
	applied              CommandLogAppliedRecorder
	terminalFailures     CommandLogTerminalFailureRecorder
	retryableFailures    CommandLogRetryableFailureRecorder
	ownerID              string
	batchSize            int
	partitionLimit       int
	partitionConcurrency int
	commitAttempts       int
	interval             time.Duration
	claimTTL             time.Duration
	claimRefreshInterval time.Duration
}

type CommandLogWorkerResult struct {
	Partition            LogPartition
	Assigned             bool
	AssignmentLost       bool
	AssignmentOwnerID    string
	AssignmentGeneration int64
	Claimed              bool
	ClaimLost            bool
	ClaimOwnerID         string
	ClaimExpiresAt       int64
	StartedOffset        int64
	LastOffset           int64
	Processed            int
	Applied              int
	TerminalFailures     int
	TerminalFailure      *proto.ErrorDetail
	CommitFailures       int
	CommitFailure        string
	FinalizerFailure     string
	RetryableFailure     *proto.ErrorDetail
}

func commandLogWorkerAssignmentResult(partition LogPartition, assignment CommandPartitionAssignment, assigned bool) CommandLogWorkerResult {
	return CommandLogWorkerResult{
		Partition:            partition.Normalize(),
		Assigned:             assigned,
		AssignmentOwnerID:    assignment.OwnerID,
		AssignmentGeneration: assignment.Generation,
	}
}

func commandLogWorkerClaimedAssignmentResult(partition LogPartition, assignment CommandPartitionAssignment, ownerID string, committedOffset int64) CommandLogWorkerResult {
	result := commandLogWorkerAssignmentResult(partition, assignment, true)
	result.Claimed = true
	result.ClaimOwnerID = ownerID
	result.StartedOffset = committedOffset
	result.LastOffset = committedOffset
	return result
}

func NewCommandLogWorker(config CommandLogWorkerConfig) *CommandLogWorker {
	partitions := config.Partitions
	if partitions == nil {
		if lister, ok := config.Log.(CommandPartitionLister); ok {
			partitions = lister
		}
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	partitionLimit := config.PartitionLimit
	if partitionLimit <= 0 {
		partitionLimit = 100
	}
	partitionConcurrency := config.PartitionConcurrency
	if partitionConcurrency <= 0 {
		partitionConcurrency = 1
	}
	commitAttempts := config.CommitAttempts
	if commitAttempts <= 0 {
		commitAttempts = 3
	}
	interval := config.Interval
	if interval <= 0 {
		interval = time.Second
	}
	claimTTL := config.ClaimTTL
	if claimTTL <= 0 {
		claimTTL = 30 * time.Second
	}
	claimRefreshInterval := config.ClaimRefreshInterval
	if claimRefreshInterval <= 0 {
		claimRefreshInterval = claimTTL / 3
		if claimRefreshInterval <= 0 {
			claimRefreshInterval = time.Second
		}
	}
	ownerID := strings.TrimSpace(config.OwnerID)
	if ownerID == "" {
		ownerID = newID("cmdw_")
	}
	terminalFailures := config.TerminalFailures
	if terminalFailures == nil {
		if recorder, ok := config.Executor.(CommandLogTerminalFailureRecorder); ok {
			terminalFailures = recorder
		}
	}
	retryableFailures := config.RetryableFailures
	if retryableFailures == nil {
		if recorder, ok := config.Executor.(CommandLogRetryableFailureRecorder); ok {
			retryableFailures = recorder
		}
	}
	applied := config.Applied
	if applied == nil {
		if recorder, ok := config.Executor.(CommandLogAppliedRecorder); ok {
			applied = recorder
		}
	}
	finalizer := config.Finalizer
	if finalizer == nil {
		finalizer = commandLogDefaultFinalizer{
			applied:           applied,
			terminalFailures:  terminalFailures,
			retryableFailures: retryableFailures,
		}
	}
	return &CommandLogWorker{
		log:                  config.Log,
		partitions:           partitions,
		assignments:          config.Assignments,
		claims:               config.Claims,
		executor:             config.Executor,
		finalizer:            finalizer,
		applied:              applied,
		terminalFailures:     terminalFailures,
		retryableFailures:    retryableFailures,
		ownerID:              ownerID,
		batchSize:            batchSize,
		partitionLimit:       partitionLimit,
		partitionConcurrency: partitionConcurrency,
		commitAttempts:       commitAttempts,
		interval:             interval,
		claimTTL:             claimTTL,
		claimRefreshInterval: claimRefreshInterval,
	}
}

func (w *CommandLogWorker) DrainOnce(ctx context.Context) ([]CommandLogWorkerResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("command log worker: nil receiver")
	}
	if w.log == nil {
		return nil, fmt.Errorf("command log worker: nil log")
	}
	if w.partitions == nil && !w.hasAssignedPartitionLister() {
		return nil, fmt.Errorf("command log worker: nil partition lister")
	}
	if w.executor == nil {
		return nil, fmt.Errorf("command log worker: nil executor")
	}
	if w.finalizer == nil {
		return nil, fmt.Errorf("command log worker: nil finalizer")
	}
	partitions, err := w.listDrainPartitions(ctx)
	if err != nil {
		return nil, err
	}
	if w.partitionConcurrency > 1 && len(partitions) > 1 {
		return w.drainPartitionsConcurrently(ctx, partitions)
	}
	return w.drainPartitionsSequentially(ctx, partitions)
}

func (w *CommandLogWorker) drainPartitionsSequentially(ctx context.Context, partitions []LogPartition) ([]CommandLogWorkerResult, error) {
	results := make([]CommandLogWorkerResult, 0, len(partitions))
	for _, partition := range partitions {
		result, err := w.drainListedPartition(ctx, partition)
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (w *CommandLogWorker) drainPartitionsConcurrently(ctx context.Context, partitions []LogPartition) ([]CommandLogWorkerResult, error) {
	concurrency := w.partitionConcurrency
	if concurrency <= 1 || len(partitions) <= 1 {
		return w.drainPartitionsSequentially(ctx, partitions)
	}
	if concurrency > len(partitions) {
		concurrency = len(partitions)
	}
	type drainOutcome struct {
		index  int
		result CommandLogWorkerResult
		err    error
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	slots := make(chan struct{}, concurrency)
	outcomes := make(chan drainOutcome, len(partitions))
	launched := 0
launch:
	for i, partition := range partitions {
		select {
		case <-workerCtx.Done():
			break launch
		case slots <- struct{}{}:
		}
		launched++
		go func(index int, partition LogPartition) {
			defer func() { <-slots }()
			result, err := w.drainListedPartition(workerCtx, partition)
			if err != nil {
				cancel()
			}
			outcomes <- drainOutcome{index: index, result: result, err: err}
		}(i, partition)
	}

	ordered := make([]CommandLogWorkerResult, len(partitions))
	present := make([]bool, len(partitions))
	var firstErr error
	for i := 0; i < launched; i++ {
		outcome := <-outcomes
		ordered[outcome.index] = outcome.result
		present[outcome.index] = true
		if outcome.err != nil &&
			(firstErr == nil || (errors.Is(firstErr, context.Canceled) && !errors.Is(outcome.err, context.Canceled))) {
			firstErr = outcome.err
		}
	}
	results := make([]CommandLogWorkerResult, 0, launched)
	for i := range ordered {
		if present[i] {
			results = append(results, ordered[i])
		}
	}
	if firstErr != nil {
		return results, firstErr
	}
	if launched < len(partitions) {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if err := workerCtx.Err(); err != nil {
			return results, err
		}
	}
	return results, nil
}

func (w *CommandLogWorker) drainListedPartition(ctx context.Context, partition LogPartition) (CommandLogWorkerResult, error) {
	partition = partition.Normalize()
	assignment, assigned, err := w.assignPartition(ctx, partition)
	if err != nil {
		return CommandLogWorkerResult{Partition: partition}, err
	}
	if !assigned {
		return commandLogWorkerAssignmentResult(partition, assignment, false), nil
	}
	claim, acquired, err := w.claimPartition(ctx, partition)
	if err != nil {
		return CommandLogWorkerResult{Partition: partition}, err
	}
	if !acquired {
		result := commandLogWorkerAssignmentResult(partition, assignment, true)
		result.ClaimOwnerID = claim.OwnerID
		result.ClaimExpiresAt = claim.ExpiresAt
		return result, nil
	}
	result, err := w.drainPartition(ctx, partition)
	if !result.AssignmentLost {
		result.Assigned = true
	}
	if result.AssignmentOwnerID == "" {
		result.AssignmentOwnerID = assignment.OwnerID
		result.AssignmentGeneration = assignment.Generation
	}
	result.Claimed = true
	if result.ClaimOwnerID == "" {
		result.ClaimOwnerID = claim.OwnerID
		result.ClaimExpiresAt = claim.ExpiresAt
	}
	return result, err
}

func (w *CommandLogWorker) hasAssignedPartitionLister() bool {
	if w == nil || w.assignments == nil {
		return false
	}
	_, ok := w.assignments.(CommandPartitionAssignmentLister)
	return ok
}

func (w *CommandLogWorker) listDrainPartitions(ctx context.Context) ([]LogPartition, error) {
	if w == nil {
		return nil, fmt.Errorf("command log worker: nil receiver")
	}
	if lister, ok := w.assignments.(CommandPartitionAssignmentLister); ok {
		assignments, err := lister.ListAssignedCommandPartitions(ctx, w.ownerID, w.partitionLimit)
		if err != nil {
			return nil, err
		}
		return commandPartitionAssignmentPartitions(assignments), nil
	}
	if w.partitions == nil {
		return nil, fmt.Errorf("command log worker: nil partition lister")
	}
	return w.partitions.ListCommandPartitions(ctx, w.partitionLimit)
}

func (w *CommandLogWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	_, _ = w.DrainOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = w.DrainOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *CommandLogWorker) claimPartition(ctx context.Context, partition LogPartition) (CommandPartitionClaim, bool, error) {
	claim := commandPartitionClaimForOwner(partition, w.ownerID, 0)
	if w.claims == nil {
		return claim, true, nil
	}
	return w.claims.ClaimCommandPartition(ctx, w.ownerID, partition, w.claimTTL)
}

func (w *CommandLogWorker) assignPartition(ctx context.Context, partition LogPartition) (CommandPartitionAssignment, bool, error) {
	assignment := commandPartitionAssignmentForOwner(partition, w.ownerID, 1)
	if w.assignments == nil {
		return assignment, true, nil
	}
	return w.assignments.AssignCommandPartition(ctx, w.ownerID, partition)
}

func (w *CommandLogWorker) shouldRefreshOwnership() bool {
	if w == nil {
		return false
	}
	if w.claims != nil {
		return true
	}
	if w.assignments == nil {
		return false
	}
	stable, ok := w.assignments.(StableCommandPartitionAssigner)
	return !ok || !stable.StableCommandPartitionAssignment()
}

func (w *CommandLogWorker) shouldHeartbeatOwnership() bool {
	return w.shouldRefreshOwnership() && w.claimRefreshInterval > 0
}

func (w *CommandLogWorker) refreshOwnershipIfNeeded(ctx context.Context, partition LogPartition, result *CommandLogWorkerResult) (bool, error) {
	if !w.shouldRefreshOwnership() {
		return true, nil
	}
	return w.refreshOwnership(ctx, partition, result)
}

func (w *CommandLogWorker) drainPartition(ctx context.Context, partition LogPartition) (CommandLogWorkerResult, error) {
	committed, err := w.log.CommittedOffset(ctx, partition)
	if err != nil {
		return CommandLogWorkerResult{Partition: partition}, err
	}
	result := CommandLogWorkerResult{
		Partition:     partition,
		StartedOffset: committed,
		LastOffset:    committed,
	}
	records, err := w.log.FetchPartition(ctx, partition, committed, w.batchSize)
	defer w.allowCommandLogRebalance()
	if err != nil {
		return result, err
	}
	if batchFinalizer, ok := w.finalizer.(CommandLogBatchFinalizer); ok {
		return result, w.drainPartitionWithBatchFinalizer(ctx, partition, records, batchFinalizer, &result)
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if record.Offset <= result.LastOffset {
			continue
		}
		if err := validateCommandLogWorkerOffsetProgress(partition, result.LastOffset, record); err != nil {
			return result, err
		}
		acquired, err := w.refreshOwnershipIfNeeded(ctx, partition, &result)
		if err != nil {
			return result, err
		}
		if !acquired {
			return result, nil
		}
		reply, acquired, err := w.executeWithOwnershipHeartbeat(ctx, partition, record, &result)
		if err != nil {
			return result, err
		}
		if !acquired {
			return result, nil
		}
		acquired, err = w.refreshOwnershipIfNeeded(ctx, partition, &result)
		if err != nil {
			return result, err
		}
		if !acquired {
			return result, nil
		}
		finalized, acquired, err := w.finalizeWithOwnershipHeartbeat(ctx, partition, record, reply, &result)
		result.Applied += finalized.Applied
		result.TerminalFailures += finalized.TerminalFailures
		if finalized.TerminalFailure != nil {
			result.TerminalFailure = finalized.TerminalFailure
		}
		result.CommitFailures += finalized.CommitFailures
		if finalized.CommitFailure != "" {
			result.CommitFailure = finalized.CommitFailure
		}
		if finalized.RetryableFailure != nil {
			result.RetryableFailure = finalized.RetryableFailure
		}
		if err != nil {
			if finalized.CommitFailure == "" {
				result.FinalizerFailure = err.Error()
			}
			if finalized.Committed {
				if commitErr := w.recordFinalizedCommit(ctx, partition, record.Offset, &result); commitErr != nil {
					return result, commitErr
				}
				advanceCommandLogWorkerResult(&result, record.Offset, 1)
			}
			return result, err
		}
		if !acquired {
			if finalized.Committed {
				if commitErr := w.recordFinalizedCommit(ctx, partition, record.Offset, &result); commitErr != nil {
					return result, commitErr
				}
				advanceCommandLogWorkerResult(&result, record.Offset, 1)
			}
			return result, nil
		}
		if finalized.RetryableFailure != nil {
			return result, nil
		}
		if finalized.Committed {
			if err := w.recordFinalizedCommit(ctx, partition, record.Offset, &result); err != nil {
				return result, err
			}
		} else {
			committed, err := w.commitPartition(ctx, partition, record.Offset, &result)
			if err != nil {
				return result, err
			}
			if !committed {
				return result, nil
			}
		}
		advanceCommandLogWorkerResult(&result, record.Offset, 1)
	}
	return result, nil
}

func (w *CommandLogWorker) drainPartitionWithBatchFinalizer(ctx context.Context, partition LogPartition, records []CommandLogRecord, batchFinalizer CommandLogBatchFinalizer, result *CommandLogWorkerResult) error {
	if result == nil {
		return fmt.Errorf("command log worker: nil result")
	}
	if batchFinalizer == nil {
		return fmt.Errorf("command log worker: nil batch finalizer")
	}
	pendingRecords := make([]CommandLogRecord, 0, len(records))
	pendingReplies := make([]Reply, 0, len(records))
	pendingLastOffset := result.LastOffset
	flushPending := func() (bool, error) {
		if len(pendingRecords) == 0 {
			return true, nil
		}
		finalized, acquired, err := w.finalizeBatchWithOwnershipHeartbeat(ctx, partition, pendingRecords, pendingReplies, batchFinalizer, result)
		applyCommandLogFinalizationResult(result, finalized)
		if err != nil {
			if finalized.CommitFailure == "" {
				result.FinalizerFailure = err.Error()
			}
			if finalized.Committed {
				if commitErr := w.recordFinalizedCommit(ctx, partition, pendingRecords[len(pendingRecords)-1].Offset, result); commitErr != nil {
					return false, commitErr
				}
				advanceCommandLogWorkerResult(result, pendingRecords[len(pendingRecords)-1].Offset, len(pendingRecords))
			}
			return false, err
		}
		if !acquired {
			if finalized.Committed {
				if commitErr := w.recordFinalizedCommit(ctx, partition, pendingRecords[len(pendingRecords)-1].Offset, result); commitErr != nil {
					return false, commitErr
				}
				advanceCommandLogWorkerResult(result, pendingRecords[len(pendingRecords)-1].Offset, len(pendingRecords))
			}
			return false, nil
		}
		if finalized.RetryableFailure != nil {
			return false, nil
		}
		if !finalized.Committed {
			err := fmt.Errorf("command log worker: batch finalizer did not commit partition %s/%s through offset %d",
				partition.Kind, partition.Key, pendingRecords[len(pendingRecords)-1].Offset)
			result.FinalizerFailure = err.Error()
			return false, err
		}
		if err := w.recordFinalizedCommit(ctx, partition, pendingRecords[len(pendingRecords)-1].Offset, result); err != nil {
			return false, err
		}
		advanceCommandLogWorkerResult(result, pendingRecords[len(pendingRecords)-1].Offset, len(pendingRecords))
		pendingRecords = pendingRecords[:0]
		pendingReplies = pendingReplies[:0]
		pendingLastOffset = result.LastOffset
		return true, nil
	}

	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.Offset <= pendingLastOffset {
			continue
		}
		if err := validateCommandLogWorkerOffsetProgress(partition, pendingLastOffset, record); err != nil {
			return err
		}
		acquired, err := w.refreshOwnershipIfNeeded(ctx, partition, result)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		reply, acquired, err := w.executeWithOwnershipHeartbeat(ctx, partition, record, result)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		acquired, err = w.refreshOwnershipIfNeeded(ctx, partition, result)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		if reply.Err != nil && reply.Err.Retryable {
			flushed, err := flushPending()
			if err != nil || !flushed {
				return err
			}
			finalized, acquired, err := w.finalizeWithOwnershipHeartbeat(ctx, partition, record, reply, result)
			applyCommandLogFinalizationResult(result, finalized)
			if err != nil {
				if finalized.CommitFailure == "" {
					result.FinalizerFailure = err.Error()
				}
				if finalized.Committed {
					if commitErr := w.recordFinalizedCommit(ctx, partition, record.Offset, result); commitErr != nil {
						return commitErr
					}
					advanceCommandLogWorkerResult(result, record.Offset, 1)
				}
				return err
			}
			if !acquired {
				if finalized.Committed {
					if commitErr := w.recordFinalizedCommit(ctx, partition, record.Offset, result); commitErr != nil {
						return commitErr
					}
					advanceCommandLogWorkerResult(result, record.Offset, 1)
				}
				return nil
			}
			if finalized.Committed {
				if err := w.recordFinalizedCommit(ctx, partition, record.Offset, result); err != nil {
					return err
				}
				advanceCommandLogWorkerResult(result, record.Offset, 1)
			}
			return nil
		}
		pendingRecords = append(pendingRecords, record)
		pendingReplies = append(pendingReplies, reply)
		pendingLastOffset = record.Offset
	}
	_, err := flushPending()
	return err
}

func (w *CommandLogWorker) allowCommandLogRebalance() {
	if w == nil || w.log == nil {
		return
	}
	allowCommandLogRebalance(w.log)
}

func allowCommandLogRebalance(commandLog CommandLog) {
	allower, ok := commandLog.(CommandLogRebalanceAllower)
	if !ok {
		return
	}
	allower.AllowCommandLogRebalance()
}

func validateCommandLogWorkerOffsetProgress(partition LogPartition, lastOffset int64, record CommandLogRecord) error {
	partition = partition.Normalize()
	if err := record.SourcePosition.ValidateForRecord(record); err != nil {
		return fmt.Errorf("command log worker: invalid source position in %s/%s offset %d: %w",
			partition.Kind, partition.Key, record.Offset, err)
	}
	if record.Offset == lastOffset+1 {
		return nil
	}
	if record.SourcePosition.IsZero() {
		return fmt.Errorf("command log worker: offset gap in %s/%s: got %d after %d",
			partition.Kind, partition.Key, record.Offset, lastOffset)
	}
	return nil
}

type commandLogFinalizationOutcome struct {
	finalized CommandLogFinalizationResult
	err       error
}

func (w *CommandLogWorker) finalizeWithOwnershipHeartbeat(ctx context.Context, partition LogPartition, record CommandLogRecord, reply Reply, result *CommandLogWorkerResult) (CommandLogFinalizationResult, bool, error) {
	if !w.shouldHeartbeatOwnership() {
		finalized, err := w.finalizer.FinalizeCommandLogRecord(ctx, record, reply)
		return finalized, true, err
	}
	finalizerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan commandLogFinalizationOutcome, 1)
	go func() {
		finalized, err := w.finalizer.FinalizeCommandLogRecord(finalizerCtx, record, reply)
		done <- commandLogFinalizationOutcome{finalized: finalized, err: err}
	}()
	ticker := time.NewTicker(w.claimRefreshInterval)
	defer ticker.Stop()
	owned := true
	var ownershipErr error
	for {
		select {
		case outcome := <-done:
			if ownershipErr != nil {
				return outcome.finalized, false, ownershipErr
			}
			if !owned {
				if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
					return outcome.finalized, false, outcome.err
				}
				return outcome.finalized, false, nil
			}
			return outcome.finalized, true, outcome.err
		case <-ticker.C:
			if !owned {
				continue
			}
			acquired, err := w.refreshOwnership(ctx, partition, result)
			if err != nil {
				ownershipErr = err
				owned = false
				cancel()
				continue
			}
			if !acquired {
				owned = false
				cancel()
			}
		case <-ctx.Done():
			cancel()
			return CommandLogFinalizationResult{}, false, ctx.Err()
		}
	}
}

func (w *CommandLogWorker) finalizeBatchWithOwnershipHeartbeat(ctx context.Context, partition LogPartition, records []CommandLogRecord, replies []Reply, finalizer CommandLogBatchFinalizer, result *CommandLogWorkerResult) (CommandLogFinalizationResult, bool, error) {
	if !w.shouldHeartbeatOwnership() {
		finalized, err := finalizer.FinalizeCommandLogBatch(ctx, records, replies)
		return finalized, true, err
	}
	finalizerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan commandLogFinalizationOutcome, 1)
	go func() {
		finalized, err := finalizer.FinalizeCommandLogBatch(finalizerCtx, records, replies)
		done <- commandLogFinalizationOutcome{finalized: finalized, err: err}
	}()
	ticker := time.NewTicker(w.claimRefreshInterval)
	defer ticker.Stop()
	owned := true
	var ownershipErr error
	for {
		select {
		case outcome := <-done:
			if ownershipErr != nil {
				return outcome.finalized, false, ownershipErr
			}
			if !owned {
				if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
					return outcome.finalized, false, outcome.err
				}
				return outcome.finalized, false, nil
			}
			return outcome.finalized, true, outcome.err
		case <-ticker.C:
			if !owned {
				continue
			}
			acquired, err := w.refreshOwnership(ctx, partition, result)
			if err != nil {
				ownershipErr = err
				owned = false
				cancel()
				continue
			}
			if !acquired {
				owned = false
				cancel()
			}
		case <-ctx.Done():
			cancel()
			return CommandLogFinalizationResult{}, false, ctx.Err()
		}
	}
}

func applyCommandLogFinalizationResult(result *CommandLogWorkerResult, finalized CommandLogFinalizationResult) {
	if result == nil {
		return
	}
	result.Applied += finalized.Applied
	result.TerminalFailures += finalized.TerminalFailures
	if finalized.TerminalFailure != nil {
		result.TerminalFailure = finalized.TerminalFailure
	}
	result.CommitFailures += finalized.CommitFailures
	if finalized.CommitFailure != "" {
		result.CommitFailure = finalized.CommitFailure
	}
	if finalized.RetryableFailure != nil {
		result.RetryableFailure = finalized.RetryableFailure
	}
}

func advanceCommandLogWorkerResult(result *CommandLogWorkerResult, offset int64, count int) {
	if result == nil || count <= 0 {
		return
	}
	result.LastOffset = offset
	result.Processed += count
}

func (w *CommandLogWorker) executeWithOwnershipHeartbeat(ctx context.Context, partition LogPartition, record CommandLogRecord, result *CommandLogWorkerResult) (Reply, bool, error) {
	if !w.shouldHeartbeatOwnership() {
		return w.executor.ExecuteCommandLogRecord(ctx, record), true, nil
	}
	done := make(chan Reply, 1)
	go func() {
		done <- w.executor.ExecuteCommandLogRecord(ctx, record)
	}()
	ticker := time.NewTicker(w.claimRefreshInterval)
	defer ticker.Stop()
	claimOwned := true
	var refreshErr error
	for {
		select {
		case reply := <-done:
			if refreshErr != nil {
				return Reply{}, false, refreshErr
			}
			return reply, claimOwned, nil
		case <-ticker.C:
			if !claimOwned {
				continue
			}
			acquired, err := w.refreshOwnership(ctx, partition, result)
			if err != nil {
				refreshErr = err
				claimOwned = false
				continue
			}
			if !acquired {
				claimOwned = false
			}
		case <-ctx.Done():
			return Reply{}, false, ctx.Err()
		}
	}
}

func (w *CommandLogWorker) commitPartition(ctx context.Context, partition LogPartition, offset int64, result *CommandLogWorkerResult) (bool, error) {
	attempts := w.commitAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		acquired, err := w.refreshOwnershipIfNeeded(ctx, partition, result)
		if err != nil {
			return false, err
		}
		if !acquired {
			return false, nil
		}
		if err := w.log.CommitPartition(ctx, partition, offset); err != nil {
			lastErr = err
			if result != nil {
				result.CommitFailures++
				result.CommitFailure = err.Error()
			}
			continue
		}
		if result != nil {
			result.CommitFailure = ""
		}
		return true, nil
	}
	return false, fmt.Errorf("command log worker: commit partition %s/%s offset %d failed after %d attempts: %w",
		partition.Kind, partition.Key, offset, attempts, lastErr)
}

func (w *CommandLogWorker) recordFinalizedCommit(ctx context.Context, partition LogPartition, offset int64, result *CommandLogWorkerResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w == nil || w.log == nil {
		return nil
	}
	recorder, ok := w.log.(CommandLogCommitRecorder)
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

type commandLogDefaultFinalizer struct {
	applied           CommandLogAppliedRecorder
	terminalFailures  CommandLogTerminalFailureRecorder
	retryableFailures CommandLogRetryableFailureRecorder
}

func (f commandLogDefaultFinalizer) FinalizeCommandLogRecord(ctx context.Context, record CommandLogRecord, reply Reply) (CommandLogFinalizationResult, error) {
	result := CommandLogFinalizationResult{}
	if reply.Err != nil && reply.Err.Retryable {
		result.RetryableFailure = reply.Err
		if f.retryableFailures != nil {
			if err := f.retryableFailures.RecordCommandLogRetryableFailure(ctx, record, reply.Err); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if reply.Err != nil {
		result.TerminalFailures++
		result.TerminalFailure = reply.Err
		if f.terminalFailures != nil {
			if err := f.terminalFailures.RecordCommandLogTerminalFailure(ctx, record, reply.Err); err != nil {
				return result, err
			}
		}
	} else if f.applied != nil && reply.Result != nil {
		if err := f.applied.RecordCommandLogApplied(ctx, record, reply.Result); err != nil {
			return result, err
		}
		result.Applied++
	}
	return result, nil
}

func (w *CommandLogWorker) refreshOwnership(ctx context.Context, partition LogPartition, result *CommandLogWorkerResult) (bool, error) {
	assigned, err := w.refreshAssignment(ctx, partition, result)
	if err != nil {
		return false, err
	}
	if !assigned {
		return false, nil
	}
	return w.refreshClaim(ctx, partition, result)
}

func (w *CommandLogWorker) refreshAssignment(ctx context.Context, partition LogPartition, result *CommandLogWorkerResult) (bool, error) {
	assignment, assigned, err := w.assignPartition(ctx, partition)
	if err != nil {
		return false, err
	}
	if result != nil {
		result.Assigned = assigned
		result.AssignmentOwnerID = assignment.OwnerID
		result.AssignmentGeneration = assignment.Generation
		if !assigned {
			result.AssignmentLost = true
		}
	}
	return assigned, nil
}

func (w *CommandLogWorker) refreshClaim(ctx context.Context, partition LogPartition, result *CommandLogWorkerResult) (bool, error) {
	claim, acquired, err := w.claimPartition(ctx, partition)
	if err != nil {
		return false, err
	}
	if result != nil {
		result.ClaimOwnerID = claim.OwnerID
		result.ClaimExpiresAt = claim.ExpiresAt
		if !acquired {
			result.ClaimLost = true
		}
	}
	return acquired, nil
}
