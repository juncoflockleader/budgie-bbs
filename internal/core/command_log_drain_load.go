package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type CommandLogDrainLoadConfig struct {
	Boards               int    `json:"boards"`
	CommandsPerBoard     int    `json:"commandsPerBoard"`
	RepliesPerThread     int    `json:"repliesPerThread"`
	DirectedReplies      bool   `json:"directedReplies"`
	SubmitConcurrency    int    `json:"submitConcurrency"`
	Writers              int    `json:"writers"`
	BatchSize            int    `json:"batchSize"`
	PartitionConcurrency int    `json:"partitionConcurrency"`
	BodyBytes            int    `json:"bodyBytes"`
	BoardPrefix          string `json:"boardPrefix"`
	UserName             string `json:"userName"`
	AssignmentMode       string `json:"assignmentMode"`
	ExecutorMode         string `json:"executorMode"`
	AuthoritativeSubmit  bool   `json:"authoritativeSubmit"`
}

const (
	CommandLogDrainAssignmentHash     = "hash-assignment"
	CommandLogDrainAssignmentSnapshot = "snapshot-assignment"
	CommandLogDrainExecutorSQL        = "sql"
	CommandLogDrainExecutorNative     = "native"

	CommandLogDrainScalarAllocatorBrokerStreamSequence = "broker-stream-sequence"
	CommandLogDrainScalarAllocatorMemoryStreamSequence = "memory-stream-sequence"
	CommandLogDrainScalarAllocatorPostgresEventSeq     = "postgres-event-seq"
	CommandLogDrainScalarAllocatorSQLEventPartitions   = "sql-event-partition-offsets"
	CommandLogDrainScalarAllocatorSQLEventOffsets      = "sql-event-scalar-offsets"
)

type CommandLogDrainLoadReport struct {
	Config                     CommandLogDrainLoadConfig            `json:"config"`
	Runtime                    CommandLogDrainLoadRuntime           `json:"runtime"`
	Evidence                   CommandLogDrainLoadEvidence          `json:"evidence"`
	StartedAt                  int64                                `json:"startedAt"`
	FinishedAt                 int64                                `json:"finishedAt"`
	TotalCommands              int                                  `json:"totalCommands"`
	Partitions                 int                                  `json:"partitions"`
	Submit                     CommandLogLoadStage                  `json:"submit"`
	Drain                      CommandLogDrainStage                 `json:"drain"`
	EventProjection            EventStoreProjectionLoadStage        `json:"eventProjection"`
	MaxPartitionLagBeforeDrain int64                                `json:"maxPartitionLagBeforeDrain"`
	MaxPartitionLagAfterDrain  int64                                `json:"maxPartitionLagAfterDrain"`
	PromotionReadiness         CommandLogPromotionReadinessReport   `json:"promotionReadiness"`
	MaterializationAudit       CommandLogMaterializationAuditReport `json:"materializationAudit"`
	ScalarCompatibilityAudit   CommandLogScalarCompatibilityAudit   `json:"scalarCompatibilityAudit"`
}

type CommandLogDrainLoadEvidence struct {
	Tool         string `json:"tool,omitempty"`
	BudgetFile   string `json:"budgetFile,omitempty"`
	BudgetSHA256 string `json:"budgetSha256,omitempty"`
	GitRevision  string `json:"gitRevision,omitempty"`
	GitModified  bool   `json:"gitModified"`
}

type CommandLogDrainLoadRuntime struct {
	CommandLogBackend            string   `json:"commandLogBackend,omitempty"`
	EventLogBackend              string   `json:"eventLogBackend,omitempty"`
	MaterializationStore         string   `json:"materializationStore,omitempty"`
	ScalarCompatibilityAllocator string   `json:"scalarCompatibilityAllocator,omitempty"`
	NATSEndpoint                 string   `json:"natsEndpoint,omitempty"`
	PostgresEndpoint             string   `json:"postgresEndpoint,omitempty"`
	RequirePostgres              bool     `json:"requirePostgres"`
	DurableStaging               bool     `json:"durableStaging"`
	PostgresSchema               string   `json:"postgresSchema,omitempty"`
	KeepPostgresSchema           bool     `json:"keepPostgresSchema"`
	CommandLogIndexBackend       string   `json:"commandLogIndexBackend,omitempty"`
	CommandLogIndexPrefix        string   `json:"commandLogIndexPrefix,omitempty"`
	RedisEndpoint                string   `json:"redisEndpoint,omitempty"`
	CommandNATSStream            string   `json:"commandNatsStream,omitempty"`
	CommandNATSReplicas          int      `json:"commandNatsReplicas,omitempty"`
	EventNATSStream              string   `json:"eventNatsStream,omitempty"`
	EventNATSReplicas            int      `json:"eventNatsReplicas,omitempty"`
	KafkaBrokers                 []string `json:"kafkaBrokers,omitempty"`
	KafkaTLS                     bool     `json:"kafkaTls,omitempty"`
	KafkaSASLMechanism           string   `json:"kafkaSaslMechanism,omitempty"`
	KafkaCommandTopic            string   `json:"kafkaCommandTopic,omitempty"`
	KafkaEventTopic              string   `json:"kafkaEventTopic,omitempty"`
	KafkaConsumerGroup           string   `json:"kafkaConsumerGroup,omitempty"`
	KafkaCommandPartitions       int      `json:"kafkaCommandPartitions,omitempty"`
	KafkaEventPartitions         int      `json:"kafkaEventPartitions,omitempty"`
}

type CommandLogScalarCompatibilityAudit struct {
	Enabled                    bool   `json:"enabled"`
	Store                      string `json:"store,omitempty"`
	OffsetID                   string `json:"offsetId,omitempty"`
	LegacySQLScalarOffsetAfter int64  `json:"legacySqlScalarOffsetAfter"`
}

type CommandLogLoadStage struct {
	Commands        int      `json:"commands"`
	Succeeded       int      `json:"succeeded"`
	Failed          int      `json:"failed"`
	DurationMS      int64    `json:"durationMs"`
	CommandsPerSec  float64  `json:"commandsPerSec"`
	SampleErrorText []string `json:"sampleErrorText,omitempty"`
}

type CommandLogDrainStage struct {
	Commands          int      `json:"commands"`
	Processed         int      `json:"processed"`
	Applied           int      `json:"applied"`
	TerminalFailures  int      `json:"terminalFailures"`
	RetryableFailures int      `json:"retryableFailures"`
	CommitFailures    int      `json:"commitFailures"`
	AssignmentLosses  int      `json:"assignmentLosses"`
	ClaimLosses       int      `json:"claimLosses"`
	Rounds            int      `json:"rounds"`
	DurationMS        int64    `json:"durationMs"`
	CommandsPerSec    float64  `json:"commandsPerSec"`
	SampleErrorText   []string `json:"sampleErrorText,omitempty"`
}

type EventStoreProjectionLoadStage struct {
	Enabled                bool     `json:"enabled"`
	Partitions             int      `json:"partitions"`
	PartitionLimit         int      `json:"partitionLimit,omitempty"`
	PartitionLimitExceeded bool     `json:"partitionLimitExceeded"`
	ExpectedEvents         int      `json:"expectedEvents,omitempty"`
	AppliedEvents          int      `json:"appliedEvents"`
	Rounds                 int      `json:"rounds"`
	DurationMS             int64    `json:"durationMs"`
	EventsPerSec           float64  `json:"eventsPerSec"`
	SampleErrorText        []string `json:"sampleErrorText,omitempty"`
}

func DefaultCommandLogDrainLoadConfig() CommandLogDrainLoadConfig {
	return CommandLogDrainLoadConfig{
		Boards:               8,
		CommandsPerBoard:     50,
		RepliesPerThread:     0,
		SubmitConcurrency:    32,
		Writers:              4,
		BatchSize:            25,
		PartitionConcurrency: 1,
		BodyBytes:            256,
		BoardPrefix:          "cmdlogload",
		UserName:             "command_log_load_admin",
		AssignmentMode:       CommandLogDrainAssignmentHash,
		ExecutorMode:         CommandLogDrainExecutorSQL,
	}
}

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

func (c *Core) RunCommandLogDrainLoad(ctx context.Context, config CommandLogDrainLoadConfig) (CommandLogDrainLoadReport, error) {
	return c.runCommandLogDrainLoad(ctx, config, NewBrokerCommandLog(NewMemoryBrokerCommandLogClient()), commandLogDrainLoadNativeStores{})
}

func (c *Core) RunCommandLogDrainLoadWithCommandLog(ctx context.Context, config CommandLogDrainLoadConfig, commandLog CommandLog) (CommandLogDrainLoadReport, error) {
	return c.runCommandLogDrainLoad(ctx, config, commandLog, commandLogDrainLoadNativeStores{})
}

func (c *Core) RunAuthoritativeCommandLogDrainLoad(ctx context.Context, config CommandLogDrainLoadConfig) (CommandLogDrainLoadReport, error) {
	config.AuthoritativeSubmit = true
	if c == nil {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil core")
	}
	return c.runCommandLogDrainLoad(ctx, config, c.commandLogAuthoritative, commandLogDrainLoadNativeStores{})
}

func (c *Core) RunNativeCommandEventProjectionLoad(ctx context.Context, config CommandLogDrainLoadConfig) (CommandLogDrainLoadReport, error) {
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

func (c *Core) RunNativeCommandEventProjectionLoadWithStores(ctx context.Context, config CommandLogDrainLoadConfig, commandLog CommandLog, transactions CommandEventTransactionStore, events EventStore) (CommandLogDrainLoadReport, error) {
	config.ExecutorMode = CommandLogDrainExecutorNative
	return c.runCommandLogDrainLoad(ctx, config, commandLog, commandLogDrainLoadNativeStores{
		transactions: transactions,
		events:       events,
	})
}

func (c *Core) runCommandLogDrainLoad(ctx context.Context, config CommandLogDrainLoadConfig, commandLog CommandLog, nativeStores commandLogDrainLoadNativeStores) (CommandLogDrainLoadReport, error) {
	if c == nil {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil core")
	}
	if config.AuthoritativeSubmit {
		if c.commandLogAuthoritative == nil {
			return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: authoritative submit mode requires an authoritative command log")
		}
		commandLog = c.commandLogAuthoritative
	} else if c.commandLogAuthoritative != nil {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: core must not already be in authoritative command-log mode")
	}
	if commandLog == nil {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: nil command log")
	}
	config = normalizeCommandLogDrainLoadConfig(config)
	if !isSupportedCommandLogDrainAssignmentMode(config.AssignmentMode) {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: unsupported assignment mode %q", config.AssignmentMode)
	}
	if !isSupportedCommandLogDrainExecutorMode(config.ExecutorMode) {
		return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: unsupported executor mode %q", config.ExecutorMode)
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative {
		if nativeStores.transactions == nil {
			return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: native executor requires command/event transactions")
		}
		if nativeStores.events == nil {
			return CommandLogDrainLoadReport{}, fmt.Errorf("command log drain load: native executor requires an event store")
		}
	}
	report := CommandLogDrainLoadReport{
		Config:        config,
		StartedAt:     nowMS(),
		TotalCommands: commandLogDrainLoadTotalCommands(config),
		Partitions:    config.Boards,
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative {
		report.EventProjection.ExpectedEvents = commandLogDrainLoadExpectedEventProjectionEvents(config)
	}

	actor, err := c.RegisterUser(config.UserName, newID("cl_"))
	if err != nil {
		return report, fmt.Errorf("register command-log load user: %w", err)
	}
	var boardIDs []string
	if config.AuthoritativeSubmit {
		boardIDs, err = c.createCommandLogDrainLoadBoards(ctx, actor, config)
	} else {
		boardIDs, err = c.createPartitionWriteLoadBoards(ctx, actor, PartitionWriteLoadConfig{
			Boards:      config.Boards,
			BoardPrefix: config.BoardPrefix,
		})
	}
	if err != nil {
		return report, err
	}
	actorsByBoard, err := c.commandLogDrainLoadActorsByBoard(actor, boardIDs, config)
	if err != nil {
		return report, err
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative && eventStoreProjectionWatermarksRequireSeed(nativeStores.events) {
		if _, err := c.seedEventStoreProjectionWatermarksFromEventPartitionOffsets(ctx, commandLogDrainLoadEventProjectionSource(config)); err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
	}

	createConfig := config
	createConfig.RepliesPerThread = 0
	createCommands := commandLogDrainLoadCreateThreadCommands(config)
	createPartitionLimit := config.Boards
	finalCommandPartitionLimit := commandLogDrainLoadCommandPartitionLimit(config)

	createSubmit, err := c.produceCommandLogDrainLoad(ctx, commandLog, actorsByBoard, boardIDs, createConfig)
	report.Submit = mergeCommandLogLoadStage(report.Submit, createSubmit)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	lagBeforeDrain, err := maxCommandLogPartitionLag(ctx, commandLog)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.MaxPartitionLagBeforeDrain = maxInt64(report.MaxPartitionLagBeforeDrain, lagBeforeDrain)
	createDrain, err := c.drainCommandLogLoad(ctx, commandLog, createConfig, nativeStores, createCommands, createPartitionLimit)
	report.Drain = mergeCommandLogDrainStage(report.Drain, createDrain)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.MaxPartitionLagAfterDrain, err = maxCommandLogPartitionLag(ctx, commandLog)
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative {
		eventProjection, err := c.projectCommandLogDrainLoadEvents(ctx, nativeStores.events, config)
		report.EventProjection = mergeEventStoreProjectionLoadStage(report.EventProjection, eventProjection)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
	}

	if config.RepliesPerThread > 0 {
		targets, err := c.commandLogDrainLoadThreadTargets(boardIDs, config)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
		replySubmit, err := c.produceCommandLogAppendPostLoad(ctx, commandLog, actorsByBoard, targets, config)
		report.Submit = mergeCommandLogLoadStage(report.Submit, replySubmit)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
		lagBeforeReplies, err := maxCommandLogPartitionLag(ctx, commandLog)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
		report.MaxPartitionLagBeforeDrain = maxInt64(report.MaxPartitionLagBeforeDrain, lagBeforeReplies)
		replyCommands := commandLogDrainLoadAppendPostCommands(config)
		replyDrain, err := c.drainCommandLogLoad(ctx, commandLog, config, nativeStores, replyCommands, finalCommandPartitionLimit)
		report.Drain = mergeCommandLogDrainStage(report.Drain, replyDrain)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
		report.MaxPartitionLagAfterDrain, err = maxCommandLogPartitionLag(ctx, commandLog)
		if err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
		if config.ExecutorMode == CommandLogDrainExecutorNative {
			eventProjection, err := c.projectCommandLogDrainLoadEvents(ctx, nativeStores.events, config)
			report.EventProjection = mergeEventStoreProjectionLoadStage(report.EventProjection, eventProjection)
			if err != nil {
				report.FinishedAt = nowMS()
				return report, err
			}
		}
	}

	if config.ExecutorMode == CommandLogDrainExecutorNative {
		if err := c.validateCommandLogDrainLoadProjections(boardIDs, config); err != nil {
			report.FinishedAt = nowMS()
			return report, err
		}
	}
	report.PromotionReadiness, err = c.CheckCommandLogPromotionReadiness(ctx, commandLog, CommandLogPromotionReadinessConfig{
		PartitionLimit: finalCommandPartitionLimit,
		BatchSize:      config.BatchSize,
	})
	if err != nil {
		report.FinishedAt = nowMS()
		return report, err
	}
	report.MaterializationAudit = report.PromotionReadiness.MaterializationAudit
	report.FinishedAt = nowMS()
	if report.Submit.Failed > 0 {
		return report, fmt.Errorf("command log drain load: command production failed %d/%d", report.Submit.Failed, report.Submit.Commands)
	}
	if report.Drain.Applied != report.TotalCommands || report.MaxPartitionLagAfterDrain != 0 {
		return report, fmt.Errorf("command log drain load: applied %d/%d with max lag %d",
			report.Drain.Applied, report.TotalCommands, report.MaxPartitionLagAfterDrain)
	}
	if report.Drain.TerminalFailures+report.Drain.RetryableFailures+report.Drain.CommitFailures > 0 {
		return report, fmt.Errorf("command log drain load: drain failures terminal=%d retryable=%d commit=%d",
			report.Drain.TerminalFailures, report.Drain.RetryableFailures, report.Drain.CommitFailures)
	}
	if config.ExecutorMode == CommandLogDrainExecutorNative && report.EventProjection.AppliedEvents != report.EventProjection.ExpectedEvents {
		return report, fmt.Errorf("command log drain load: projected %d broker events, want %d for %d native commands",
			report.EventProjection.AppliedEvents, report.EventProjection.ExpectedEvents, report.TotalCommands)
	}
	if !report.PromotionReadiness.Ready {
		return report, fmt.Errorf("command log drain load: promotion readiness failed lagging=%d totalLag=%d missing=%d retrying=%d missingRecords=%d",
			report.PromotionReadiness.LaggingPartitions,
			report.PromotionReadiness.TotalLag,
			report.MaterializationAudit.MissingMaterialization,
			report.MaterializationAudit.RetryingCommitted,
			report.MaterializationAudit.MissingRecords)
	}
	if !report.MaterializationAudit.Complete {
		return report, fmt.Errorf("command log drain load: materialization audit incomplete missing=%d retrying=%d missingRecords=%d",
			report.MaterializationAudit.MissingMaterialization,
			report.MaterializationAudit.RetryingCommitted,
			report.MaterializationAudit.MissingRecords)
	}
	return report, nil
}

func normalizeCommandLogDrainLoadConfig(config CommandLogDrainLoadConfig) CommandLogDrainLoadConfig {
	def := DefaultCommandLogDrainLoadConfig()
	if config.Boards <= 0 {
		config.Boards = def.Boards
	}
	if config.CommandsPerBoard <= 0 {
		config.CommandsPerBoard = def.CommandsPerBoard
	}
	if config.RepliesPerThread < 0 {
		config.RepliesPerThread = 0
	}
	if config.SubmitConcurrency <= 0 {
		config.SubmitConcurrency = def.SubmitConcurrency
	}
	if config.Writers <= 0 {
		config.Writers = def.Writers
	}
	if config.BatchSize <= 0 {
		config.BatchSize = def.BatchSize
	}
	if config.PartitionConcurrency <= 0 {
		config.PartitionConcurrency = def.PartitionConcurrency
	}
	if config.BodyBytes < 0 {
		config.BodyBytes = def.BodyBytes
	}
	if config.BoardPrefix == "" {
		config.BoardPrefix = def.BoardPrefix
	}
	if config.UserName == "" {
		config.UserName = def.UserName
	}
	config.AssignmentMode = normalizeCommandLogDrainAssignmentMode(config.AssignmentMode)
	config.ExecutorMode = normalizeCommandLogDrainExecutorMode(config.ExecutorMode)
	total := commandLogDrainLoadTotalCommands(config)
	if config.SubmitConcurrency > total {
		config.SubmitConcurrency = total
	}
	if config.SubmitConcurrency <= 0 {
		config.SubmitConcurrency = 1
	}
	partitionLimit := commandLogDrainLoadCommandPartitionLimit(config)
	if config.Writers > partitionLimit {
		config.Writers = partitionLimit
	}
	if config.Writers <= 0 {
		config.Writers = 1
	}
	return config
}

func commandLogDrainLoadCreateThreadCommands(config CommandLogDrainLoadConfig) int {
	return config.Boards * config.CommandsPerBoard
}

func commandLogDrainLoadAppendPostCommands(config CommandLogDrainLoadConfig) int {
	return commandLogDrainLoadCreateThreadCommands(config) * config.RepliesPerThread
}

func commandLogDrainLoadTotalCommands(config CommandLogDrainLoadConfig) int {
	return commandLogDrainLoadCreateThreadCommands(config) + commandLogDrainLoadAppendPostCommands(config)
}

func (c *Core) commandLogDrainLoadActorsByBoard(actor *User, boardIDs []string, config CommandLogDrainLoadConfig) (map[string]*User, error) {
	actorsByBoard := make(map[string]*User, len(boardIDs))
	for _, boardID := range boardIDs {
		actorsByBoard[boardID] = actor
	}
	if actor == nil || len(boardIDs) <= 1 || config.ExecutorMode != CommandLogDrainExecutorNative || currentSQLFlavor != postgresFlavor {
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

func commandLogDrainLoadActorForBoard(actorsByBoard map[string]*User, boardID string) (*User, error) {
	actor := actorsByBoard[boardID]
	if actor == nil {
		return nil, fmt.Errorf("command log drain load: missing actor for board %s", boardID)
	}
	return actor, nil
}

func commandLogDrainLoadExpectedEventProjectionEvents(config CommandLogDrainLoadConfig) int {
	return commandLogDrainLoadCreateThreadCommands(config)*2 + commandLogDrainLoadAppendPostCommands(config)
}

func commandLogDrainLoadCommandPartitionLimit(config CommandLogDrainLoadConfig) int {
	partitions := config.Boards
	if config.RepliesPerThread > 0 {
		partitions += commandLogDrainLoadCreateThreadCommands(config)
	}
	if partitions <= 0 {
		return 1
	}
	return partitions
}

func normalizeCommandLogDrainAssignmentMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "hash", "hash-assignment":
		return CommandLogDrainAssignmentHash
	case "snapshot", "snapshot-assignment", "broker-snapshot", "consumer-group":
		return CommandLogDrainAssignmentSnapshot
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func isSupportedCommandLogDrainAssignmentMode(mode string) bool {
	return mode == CommandLogDrainAssignmentHash || mode == CommandLogDrainAssignmentSnapshot
}

func normalizeCommandLogDrainExecutorMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "sql", "postgres", "postgresql":
		return CommandLogDrainExecutorSQL
	case "native", "broker-native", "event-transaction":
		return CommandLogDrainExecutorNative
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func isSupportedCommandLogDrainExecutorMode(mode string) bool {
	return mode == CommandLogDrainExecutorSQL || mode == CommandLogDrainExecutorNative
}

func (c *Core) createCommandLogDrainLoadBoards(ctx context.Context, actor *User, config CommandLogDrainLoadConfig) ([]string, error) {
	boardIDs := make([]string, 0, config.Boards)
	for i := 0; i < config.Boards; i++ {
		boardID := fmt.Sprintf("%s_%02d", sanitizeLoadID(config.BoardPrefix), i)
		payload, err := json.Marshal(proto.CreateBoardPayload{
			ID:          boardID,
			Name:        fmt.Sprintf("Load %02d", i),
			Description: "Command-log drain load fixture",
		})
		if err != nil {
			return nil, err
		}
		reply := c.handler.ExecutePartition(ctx, actor, proto.CmdCreateBoard, payload, fmt.Sprintf("cmdlog-load-board-%d", i), CommandPartition{
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

func (c *Core) produceCommandLogDrainLoad(ctx context.Context, commandLog CommandLog, actorsByBoard map[string]*User, boardIDs []string, config CommandLogDrainLoadConfig) (CommandLogLoadStage, error) {
	stage := CommandLogLoadStage{
		Commands: commandLogDrainLoadCreateThreadCommands(config),
	}
	if stage.Commands <= 0 {
		return stage, nil
	}
	type submitResult struct {
		errText string
	}
	jobs := make(chan int)
	results := make(chan submitResult, stage.Commands)
	workers := config.SubmitConcurrency
	var wg sync.WaitGroup
	body := loadBody(config.BodyBytes)
	start := time.Now()
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range jobs {
				boardID := boardIDs[i%len(boardIDs)]
				actor, err := commandLogDrainLoadActorForBoard(actorsByBoard, boardID)
				if err != nil {
					results <- submitResult{errText: err.Error()}
					continue
				}
				payload, err := json.Marshal(proto.CreateThreadPayload{
					Board: boardID,
					Title: fmt.Sprintf("command-log load %06d", i),
					Body:  body,
				})
				if err != nil {
					results <- submitResult{errText: err.Error()}
					continue
				}
				cid := fmt.Sprintf("command-log-load-%d-%d", workerID, i)
				if config.AuthoritativeSubmit {
					reply := c.ExecCmd(ctx, actor, proto.CmdCreateThread, payload, cid)
					if reply.Err != nil {
						results <- submitResult{errText: reply.Err.Message}
						continue
					}
					if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset <= 0 {
						results <- submitResult{errText: "authoritative submit did not return a pending command-log receipt"}
						continue
					}
				} else {
					_, err = commandLog.Produce(ctx, CommandLogRecord{
						Partition:  LogPartition{Kind: partitionBoard, Key: boardID},
						ActorID:    actor.ID,
						CID:        cid,
						Command:    proto.CmdCreateThread,
						Payload:    payload,
						EnqueuedAt: nowMS(),
					})
					if err != nil {
						results <- submitResult{errText: err.Error()}
						continue
					}
				}
				results <- submitResult{}
			}
		}(worker)
	}
	for i := 0; i < stage.Commands; i++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(results)
			stage.DurationMS = time.Since(start).Milliseconds()
			return stage, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	elapsed := time.Since(start)
	stage.DurationMS = elapsed.Milliseconds()
	errSamples := map[string]bool{}
	for result := range results {
		if result.errText != "" {
			stage.Failed++
			if len(stage.SampleErrorText) < 5 && !errSamples[result.errText] {
				stage.SampleErrorText = append(stage.SampleErrorText, result.errText)
				errSamples[result.errText] = true
			}
		} else {
			stage.Succeeded++
		}
	}
	if elapsed > 0 {
		stage.CommandsPerSec = float64(stage.Succeeded) / elapsed.Seconds()
	}
	if stage.Failed > 0 {
		return stage, fmt.Errorf("produce command-log load failed %d/%d commands", stage.Failed, stage.Commands)
	}
	return stage, nil
}

func (c *Core) produceCommandLogAppendPostLoad(ctx context.Context, commandLog CommandLog, actorsByBoard map[string]*User, targets []commandLogDrainLoadThreadTarget, config CommandLogDrainLoadConfig) (CommandLogLoadStage, error) {
	stage := CommandLogLoadStage{
		Commands: len(targets) * config.RepliesPerThread,
	}
	if stage.Commands <= 0 {
		return stage, nil
	}
	type submitResult struct {
		errText string
	}
	jobs := make(chan int)
	results := make(chan submitResult, stage.Commands)
	workers := config.SubmitConcurrency
	var wg sync.WaitGroup
	body := loadBody(config.BodyBytes)
	start := time.Now()
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range jobs {
				target := targets[i/config.RepliesPerThread]
				actor, err := commandLogDrainLoadActorForBoard(actorsByBoard, target.boardID)
				if err != nil {
					results <- submitResult{errText: err.Error()}
					continue
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
					results <- submitResult{errText: err.Error()}
					continue
				}
				cid := fmt.Sprintf("command-log-reply-load-%d-%d-%d", workerID, i, replyIndex)
				if config.AuthoritativeSubmit {
					reply := c.ExecCmd(ctx, actor, proto.CmdAppendPost, raw, cid)
					if reply.Err != nil {
						results <- submitResult{errText: reply.Err.Message}
						continue
					}
					if reply.Result == nil || reply.Result.Status != proto.AckStatusPending || reply.Result.CommandOffset <= 0 {
						results <- submitResult{errText: "authoritative submit did not return a pending command-log receipt"}
						continue
					}
				} else {
					_, err = commandLog.Produce(ctx, CommandLogRecord{
						Partition:  LogPartition{Kind: partitionThread, Key: target.threadID},
						ActorID:    actor.ID,
						CID:        cid,
						Command:    proto.CmdAppendPost,
						Payload:    raw,
						EnqueuedAt: nowMS(),
					})
					if err != nil {
						results <- submitResult{errText: err.Error()}
						continue
					}
				}
				results <- submitResult{}
			}
		}(worker)
	}
	for i := 0; i < stage.Commands; i++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(results)
			stage.DurationMS = time.Since(start).Milliseconds()
			return stage, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	elapsed := time.Since(start)
	stage.DurationMS = elapsed.Milliseconds()
	errSamples := map[string]bool{}
	for result := range results {
		if result.errText != "" {
			stage.Failed++
			if len(stage.SampleErrorText) < 5 && !errSamples[result.errText] {
				stage.SampleErrorText = append(stage.SampleErrorText, result.errText)
				errSamples[result.errText] = true
			}
		} else {
			stage.Succeeded++
		}
	}
	if elapsed > 0 {
		stage.CommandsPerSec = float64(stage.Succeeded) / elapsed.Seconds()
	}
	if stage.Failed > 0 {
		return stage, fmt.Errorf("produce command-log appendPost load failed %d/%d commands", stage.Failed, stage.Commands)
	}
	return stage, nil
}

func (c *Core) drainCommandLogLoad(ctx context.Context, commandLog CommandLog, config CommandLogDrainLoadConfig, nativeStores commandLogDrainLoadNativeStores, expectedCommands, partitionLimit int) (CommandLogDrainStage, error) {
	stage := CommandLogDrainStage{
		Commands: expectedCommands,
	}
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
				if config.ExecutorMode == CommandLogDrainExecutorNative {
					if batchStore, ok := nativeStores.transactions.(CommandEventTransactionBatchStore); ok {
						nativeExecutor := NewCommandLogNativeDecisionExecutor(c)
						drainResults, err := c.drainCommandLogLoadNativeBatchMember(ctx, commandLog, assigner, member, config, batchStore, nativeExecutor, partitionLimit)
						results <- commandLogLoadDrainResult{results: drainResults, err: err}
						return
					}
				}
				executor := CommandLogExecutor(c)
				var finalizer CommandLogFinalizer
				if config.ExecutorMode == CommandLogDrainExecutorNative {
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
				addCommandLogDrainSample(&stage, errSamples, workerResult.err.Error())
				return stage, workerResult.err
			}
		}
		if stage.Processed == beforeProcessed {
			noProgressRounds++
			if noProgressRounds >= 3 {
				err := fmt.Errorf("command log drain load: no worker progress after %d rounds with lag %d", noProgressRounds, lag)
				addCommandLogDrainSample(&stage, errSamples, err.Error())
				return stage, err
			}
		} else {
			noProgressRounds = 0
		}
	}
	elapsed := time.Since(start)
	stage.DurationMS = elapsed.Milliseconds()
	if elapsed > 0 {
		stage.CommandsPerSec = float64(stage.Applied) / elapsed.Seconds()
	}
	return stage, nil
}

type commandLogDrainLoadNativePendingRecord struct {
	record     CommandLogRecord
	reply      Reply
	eventStart int
	eventEnd   int
}

type commandLogDrainLoadNativePendingPartition struct {
	result  CommandLogWorkerResult
	records []commandLogDrainLoadNativePendingRecord
	events  []EventAppend
}

type commandLogDrainLoadMemberPartitionCursor struct {
	partition       LogPartition
	committedOffset int64
}

func (c *Core) drainCommandLogLoadNativeBatchMember(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, config CommandLogDrainLoadConfig, transactions CommandEventTransactionBatchStore, executor *CommandLogNativeDecisionExecutor, partitionLimit int) ([]CommandLogWorkerResult, error) {
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
	markPendingCommitFailure := func(err error) {
		if err == nil {
			return
		}
		for i := range pending {
			pending[i].result.CommitFailures++
			pending[i].result.CommitFailure = err.Error()
		}
	}
	markPendingAppliedFailure := func(err error) {
		if err == nil {
			return
		}
		for i := range pending {
			for _, pendingRecord := range pending[i].records {
				if pendingRecord.reply.Err == nil && pendingRecord.reply.Result != nil {
					pending[i].result.FinalizerFailure = err.Error()
					break
				}
			}
		}
	}
	flushPending := func() error {
		if len(pending) == 0 {
			return nil
		}
		txs := make([]CommandEventTransaction, 0, len(pending))
		for _, partition := range pending {
			if len(partition.records) == 0 {
				continue
			}
			last := partition.records[len(partition.records)-1].record
			txs = append(txs, CommandEventTransaction{
				CommandPartition:      last.Partition,
				CommandOffset:         last.Offset,
				CommandSourcePosition: last.SourcePosition,
				Events:                partition.events,
			})
		}
		committed, err := transactions.CommitCommandEventBatch(ctx, txs)
		if err != nil {
			markPendingCommitFailure(err)
			return err
		}
		if len(committed) != len(pending) {
			err := fmt.Errorf("command log drain load: batch committed %d partitions for %d pending partitions", len(committed), len(pending))
			markPendingCommitFailure(err)
			return err
		}
		appliedRecords := []CommandLogRecord{}
		appliedResults := []*proto.AckResult{}
		for i := range pending {
			partition := &pending[i]
			result := &partition.result
			committedResult := committed[i]
			if committedResult.CommittedPartition.Normalize() != result.Partition.Normalize() {
				err := fmt.Errorf("command log drain load: committed partition %s/%s for pending partition %s/%s",
					committedResult.CommittedPartition.Normalize().Kind, committedResult.CommittedPartition.Normalize().Key,
					result.Partition.Kind, result.Partition.Key)
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
				markPendingAppliedFailure(err)
				return err
			}
		}
		return nil
	}

	for _, cursor := range cursors {
		partition := cursor.partition.Normalize()
		assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
		if err != nil {
			return results, err
		}
		if !assigned {
			results = append(results, CommandLogWorkerResult{
				Partition:            partition,
				Assigned:             false,
				AssignmentOwnerID:    assignment.OwnerID,
				AssignmentGeneration: assignment.Generation,
			})
			continue
		}
		committed := cursor.committedOffset
		result := CommandLogWorkerResult{
			Partition:            partition,
			Assigned:             true,
			AssignmentOwnerID:    assignment.OwnerID,
			AssignmentGeneration: assignment.Generation,
			Claimed:              true,
			ClaimOwnerID:         ownerID,
			StartedOffset:        committed,
			LastOffset:           committed,
		}
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
				if len(pendingPartition.records) > 0 {
					pending = append(pending, pendingPartition)
				} else {
					results = append(results, pendingPartition.result)
				}
				if err := flushPending(); err != nil {
					for _, partition := range pending {
						results = append(results, partition.result)
					}
					return results, err
				}
				for _, partition := range pending {
					results = append(results, partition.result)
				}
				pending = nil
				retryResult := CommandLogWorkerResult{
					Partition:            partition,
					Assigned:             true,
					AssignmentOwnerID:    assignment.OwnerID,
					AssignmentGeneration: assignment.Generation,
					Claimed:              true,
					ClaimOwnerID:         ownerID,
					StartedOffset:        committed,
					LastOffset:           pendingLastOffset,
					RetryableFailure:     reply.Err,
				}
				if err := c.RecordCommandLogRetryableFailure(ctx, record, reply.Err); err != nil {
					retryResult.FinalizerFailure = err.Error()
					results = append(results, retryResult)
					return results, err
				}
				results = append(results, retryResult)
				return results, nil
			}
			eventStart := len(pendingPartition.events)
			if reply.Err == nil {
				events, err := executor.DecideCommandLogEvents(ctx, record, reply)
				if err != nil {
					results = append(results, pendingPartition.result)
					return results, err
				}
				pendingPartition.events = append(pendingPartition.events, events...)
			}
			eventEnd := len(pendingPartition.events)
			pendingPartition.records = append(pendingPartition.records, commandLogDrainLoadNativePendingRecord{
				record:     record,
				reply:      reply,
				eventStart: eventStart,
				eventEnd:   eventEnd,
			})
			pendingLastOffset = record.Offset
		}
		if len(pendingPartition.records) > 0 {
			pending = append(pending, pendingPartition)
		} else {
			results = append(results, result)
		}
	}
	if err := flushPending(); err != nil {
		for _, partition := range pending {
			results = append(results, partition.result)
		}
		return results, err
	}
	for _, partition := range pending {
		results = append(results, partition.result)
	}
	return results, nil
}

func commandLogDrainLoadMemberPartitionCursors(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, partitionLimit int) ([]commandLogDrainLoadMemberPartitionCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offsetLister, ok := commandLog.(CommandPartitionOffsetLister); ok {
		offsets, err := offsetLister.ListCommandPartitionOffsets(ctx, partitionLimit)
		if err != nil {
			return nil, err
		}
		ownerID = strings.TrimSpace(ownerID)
		cursors := make([]commandLogDrainLoadMemberPartitionCursor, 0, len(offsets))
		seen := map[LogPartition]bool{}
		for _, offset := range offsets {
			if offset.TailOffset <= offset.CommittedOffset {
				continue
			}
			partition := offset.Partition.Normalize()
			if seen[partition] {
				continue
			}
			assignment, assigned, err := assigner.AssignCommandPartition(ctx, ownerID, partition)
			if err != nil {
				return nil, err
			}
			if !assigned || assignment.OwnerID != ownerID {
				continue
			}
			seen[partition] = true
			if offset.CommittedOffset < 0 {
				offset.CommittedOffset = 0
			}
			cursors = append(cursors, commandLogDrainLoadMemberPartitionCursor{
				partition:       partition,
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

func commandLogDrainLoadMemberPartitions(ctx context.Context, commandLog CommandLog, assigner CommandPartitionAssigner, ownerID string, partitionLimit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lister, ok := assigner.(CommandPartitionAssignmentLister); ok {
		assignments, err := lister.ListAssignedCommandPartitions(ctx, ownerID, partitionLimit)
		if err != nil {
			return nil, err
		}
		partitions := make([]LogPartition, 0, len(assignments))
		seen := map[LogPartition]bool{}
		for _, assignment := range assignments {
			partition := assignment.Partition.Normalize()
			if seen[partition] {
				continue
			}
			seen[partition] = true
			partitions = append(partitions, partition)
		}
		return partitions, nil
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

func newCommandLogDrainLoadAssigner(ctx context.Context, commandLog CommandLog, members []string, config CommandLogDrainLoadConfig, partitionLimit int) (CommandPartitionAssigner, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("command log drain load: assignment requires at least one writer")
	}
	switch normalizeCommandLogDrainAssignmentMode(config.AssignmentMode) {
	case CommandLogDrainAssignmentHash:
		return NewHashCommandPartitionAssigner(members, 1), nil
	case CommandLogDrainAssignmentSnapshot:
		lister, ok := commandLog.(CommandPartitionOffsetLister)
		if !ok {
			return nil, fmt.Errorf("command log drain load: snapshot assignment requires command partition offsets")
		}
		snapshot, err := snapshotCommandLogDrainLoadPartitionOffsets(ctx, lister, partitionLimit)
		if err != nil {
			return nil, err
		}
		return commandLogDrainLoadSnapshotAssigner{
			members:        append([]string(nil), members...),
			offsets:        snapshot,
			partitionLimit: partitionLimit,
			generation:     1,
		}, nil
	default:
		return nil, fmt.Errorf("command log drain load: unsupported assignment mode %q", config.AssignmentMode)
	}
}

type commandLogDrainLoadPartitionOffsetSnapshot []CommandPartitionOffset

func snapshotCommandLogDrainLoadPartitionOffsets(ctx context.Context, lister CommandPartitionOffsetLister, limit int) (commandLogDrainLoadPartitionOffsetSnapshot, error) {
	if lister == nil {
		return nil, fmt.Errorf("command log drain load: nil partition offsets")
	}
	offsets, err := lister.ListCommandPartitionOffsets(ctx, limit)
	if err != nil {
		return nil, err
	}
	return commandLogDrainLoadPartitionOffsetSnapshot(offsets).clone(limit), nil
}

func (s commandLogDrainLoadPartitionOffsetSnapshot) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.clone(limit), nil
}

func (s commandLogDrainLoadPartitionOffsetSnapshot) clone(limit int) commandLogDrainLoadPartitionOffsetSnapshot {
	if limit > 0 && len(s) > limit {
		s = s[:limit]
	}
	out := make(commandLogDrainLoadPartitionOffsetSnapshot, 0, len(s))
	for _, offset := range s {
		offset.Partition = offset.Partition.Normalize()
		if offset.TailOffset < 0 {
			offset.TailOffset = 0
		}
		if offset.CommittedOffset < 0 {
			offset.CommittedOffset = 0
		}
		if offset.CommittedOffset > offset.TailOffset {
			offset.CommittedOffset = offset.TailOffset
		}
		out = append(out, offset)
	}
	return out
}

type commandLogDrainLoadSnapshotAssigner struct {
	members        []string
	offsets        CommandPartitionOffsetLister
	partitionLimit int
	generation     int64
}

func (a commandLogDrainLoadSnapshotAssigner) StableCommandPartitionAssignment() bool {
	return true
}

func (a commandLogDrainLoadSnapshotAssigner) AssignCommandPartition(ctx context.Context, ownerID string, partition LogPartition) (CommandPartitionAssignment, bool, error) {
	if err := ctx.Err(); err != nil {
		return CommandPartitionAssignment{}, false, err
	}
	owner := a.owner(partition)
	assignment := CommandPartitionAssignment{
		Partition:  partition.Normalize(),
		OwnerID:    owner,
		Generation: a.generation,
	}
	return assignment, owner != "" && owner == strings.TrimSpace(ownerID), nil
}

func (a commandLogDrainLoadSnapshotAssigner) ListAssignedCommandPartitions(ctx context.Context, ownerID string, limit int) ([]CommandPartitionAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	offsets, err := a.ListAssignedCommandPartitionOffsets(ctx, ownerID, limit)
	if err != nil {
		return nil, err
	}
	ownerID = strings.TrimSpace(ownerID)
	assignments := make([]CommandPartitionAssignment, 0, len(offsets))
	for _, offset := range offsets {
		assignments = append(assignments, CommandPartitionAssignment{
			Partition:  offset.Partition.Normalize(),
			OwnerID:    ownerID,
			Generation: a.generation,
		})
	}
	return assignments, nil
}

func (a commandLogDrainLoadSnapshotAssigner) ListAssignedCommandPartitionOffsets(ctx context.Context, ownerID string, limit int) ([]CommandPartitionOffset, error) {
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
	offsets, err := a.offsets.ListCommandPartitionOffsets(ctx, queryLimit)
	if err != nil {
		return nil, err
	}
	ownerID = strings.TrimSpace(ownerID)
	assignedOffsets := make([]CommandPartitionOffset, 0, len(offsets))
	for _, offset := range offsets {
		if offset.TailOffset <= offset.CommittedOffset {
			continue
		}
		partition := offset.Partition.Normalize()
		owner := a.owner(partition)
		if owner == "" || owner != ownerID {
			continue
		}
		offset.Partition = partition
		assignedOffsets = append(assignedOffsets, offset)
	}
	return assignedOffsets, nil
}

func (a commandLogDrainLoadSnapshotAssigner) owner(partition LogPartition) string {
	if len(a.members) == 0 {
		return ""
	}
	partition = partition.Normalize()
	return a.members[commandPartitionAssignmentIndex(partition, len(a.members))]
}

func (c *Core) projectCommandLogDrainLoadEvents(ctx context.Context, eventStore EventStore, config CommandLogDrainLoadConfig) (EventStoreProjectionLoadStage, error) {
	partitionLimit := commandLogDrainLoadCommandPartitionLimit(config)
	stage := EventStoreProjectionLoadStage{Enabled: true, PartitionLimit: partitionLimit}
	lister, ok := eventStore.(EventPartitionLister)
	if !ok {
		err := fmt.Errorf("command log drain load: event store does not expose partitions")
		addEventProjectionLoadSample(&stage, err.Error())
		return stage, err
	}
	partitions, limited, err := listEventStoreProjectionPartitions(ctx, lister, partitionLimit)
	if err != nil {
		addEventProjectionLoadSample(&stage, err.Error())
		return stage, err
	}
	stage.Partitions = len(partitions)
	stage.PartitionLimitExceeded = limited
	if limited {
		err := fmt.Errorf("command log drain load: event projection partition limit %d did not cover every broker event partition", partitionLimit)
		addEventProjectionLoadSample(&stage, err.Error())
		return stage, err
	}
	targetOffsets, hasTargetOffsets, err := eventStoreProjectionTargetOffsets(ctx, eventStore, partitions, partitionLimit)
	if err != nil {
		addEventProjectionLoadSample(&stage, err.Error())
		return stage, err
	}
	start := time.Now()
	seenPartitions := map[LogPartition]bool{}
	projectionSource := commandLogDrainLoadEventProjectionSource(config)
	projectionConcurrency := 1
	if currentSQLFlavor == postgresFlavor {
		projectionConcurrency = config.Writers
	}
	for {
		if err := ctx.Err(); err != nil {
			stage.DurationMS = time.Since(start).Milliseconds()
			addEventProjectionLoadSample(&stage, err.Error())
			return stage, err
		}
		stage.Rounds++
		results, err := c.materializeCommandLogDrainLoadEventPartitions(ctx, eventStore, partitions, projectionSource, config.BatchSize, projectionConcurrency)
		if err != nil {
			stage.DurationMS = time.Since(start).Milliseconds()
			addEventProjectionLoadSample(&stage, err.Error())
			return stage, err
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
	if stage.DurationMS > 0 {
		stage.EventsPerSec = float64(stage.AppliedEvents) / (float64(stage.DurationMS) / 1000)
	}
	return stage, nil
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
	for partition, targetOffset := range targets {
		if targetOffset <= 0 {
			continue
		}
		if offsets[partition.Normalize()] < targetOffset {
			return false
		}
	}
	return true
}

func eventStoreProjectionTargetOffsets(ctx context.Context, eventStore EventStore, partitions []LogPartition, limit int) (map[LogPartition]int64, bool, error) {
	lister, ok := eventStore.(EventPartitionOffsetLister)
	if !ok {
		return nil, false, nil
	}
	queryLimit := limit
	if limit > 0 {
		queryLimit = limit + 1
	}
	offsets, err := lister.ListEventPartitionOffsets(ctx, queryLimit)
	if err != nil {
		return nil, false, err
	}
	if limit > 0 && len(offsets) > limit {
		return nil, false, fmt.Errorf("event projection partition offset limit %d did not cover every broker event partition", limit)
	}
	partitionsByKey := make(map[LogPartition]bool, len(partitions))
	for _, partition := range partitions {
		partitionsByKey[partition.Normalize()] = true
	}
	targets := make(map[LogPartition]int64, len(partitions))
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		if !partitionsByKey[partition] {
			continue
		}
		if offset.LastOffset > targets[partition] {
			targets[partition] = offset.LastOffset
		}
	}
	for _, partition := range partitions {
		partition = partition.Normalize()
		if _, ok := targets[partition]; !ok {
			targets[partition] = 0
		}
	}
	return targets, true, nil
}

func (c *Core) eventStoreProjectionTargetsReached(ctx context.Context, source string, targets map[LogPartition]int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c == nil || c.DB == nil {
		return false, fmt.Errorf("event projection target check: core is not initialized")
	}
	for partition, targetOffset := range targets {
		if targetOffset <= 0 {
			continue
		}
		watermark := eventStoreProjectionWatermarkName(source, partition)
		applied, found, err := lookupDerivedViewAppliedSeq(c.DB, watermark)
		if err != nil {
			return false, err
		}
		if !found || applied < targetOffset {
			return false, nil
		}
	}
	return true, nil
}

func listEventStoreProjectionPartitions(ctx context.Context, lister EventPartitionLister, limit int) ([]LogPartition, bool, error) {
	if lister == nil {
		return nil, false, fmt.Errorf("nil event partition lister")
	}
	queryLimit := limit
	if limit > 0 {
		queryLimit = limit + 1
	}
	partitions, err := lister.ListEventPartitions(ctx, queryLimit)
	if err != nil {
		return nil, false, err
	}
	limited := limit > 0 && len(partitions) > limit
	if limited {
		partitions = partitions[:limit]
	}
	out := make([]LogPartition, 0, len(partitions))
	seen := map[LogPartition]bool{}
	for _, partition := range partitions {
		partition = partition.Normalize()
		if seen[partition] {
			continue
		}
		seen[partition] = true
		out = append(out, partition)
	}
	return out, limited, nil
}

type staticEventPartitionLister []LogPartition

func (l staticEventPartitionLister) ListEventPartitions(ctx context.Context, limit int) ([]LogPartition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	partitions := make([]LogPartition, 0, len(l))
	for _, partition := range l {
		partitions = append(partitions, partition.Normalize())
	}
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions, nil
}

func (c *Core) commandLogDrainLoadThreadTargets(boardIDs []string, config CommandLogDrainLoadConfig) ([]commandLogDrainLoadThreadTarget, error) {
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
		  WHERE t.board IN (`+commandLogDrainLoadPlaceholders(len(boardIDs))+`)
		  ORDER BY t.board, t.last_seq DESC, t.id`,
		commandLogDrainLoadArgs(boardIDs)...,
	)
	if err != nil {
		return nil, fmt.Errorf("command log drain load: list projected reply targets: %w", err)
	}
	defer rows.Close()

	targets := make([]commandLogDrainLoadThreadTarget, 0, commandLogDrainLoadCreateThreadCommands(config))
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
			  WHERE thread IN (`+commandLogDrainLoadPlaceholders(len(chunk))+`)
			  ORDER BY thread, created_seq, id`,
			commandLogDrainLoadArgs(chunk)...,
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

func commandLogDrainLoadPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String()
}

func commandLogDrainLoadArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func commandLogDrainLoadEventProjectionSource(config CommandLogDrainLoadConfig) string {
	return "command-log-drain-load:" + normalizeCommandLogDrainExecutorMode(config.ExecutorMode)
}

func eventStoreProjectionWatermarksRequireSeed(store EventStore) bool {
	seeder, ok := store.(interface {
		RequiresEventStoreProjectionWatermarkSeed() bool
	})
	return ok && seeder.RequiresEventStoreProjectionWatermarkSeed()
}

func (c *Core) validateCommandLogDrainLoadProjections(boardIDs []string, config CommandLogDrainLoadConfig) error {
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

func accumulateCommandLogDrainWorkerResults(stage *CommandLogDrainStage, errSamples map[string]bool, results []CommandLogWorkerResult) {
	if stage == nil {
		return
	}
	for _, result := range results {
		stage.Processed += result.Processed
		stage.Applied += result.Applied
		stage.TerminalFailures += result.TerminalFailures
		stage.CommitFailures += result.CommitFailures
		if result.TerminalFailure != nil {
			addCommandLogDrainSample(stage, errSamples, fmt.Sprintf("%s/%s terminal: %s",
				result.Partition.Kind, result.Partition.Key, commandLogDrainFailureMessage(result.TerminalFailure)))
		}
		if result.RetryableFailure != nil {
			stage.RetryableFailures++
			addCommandLogDrainSample(stage, errSamples, fmt.Sprintf("%s/%s retryable: %s",
				result.Partition.Kind, result.Partition.Key, commandLogDrainFailureMessage(result.RetryableFailure)))
		}
		if result.AssignmentLost {
			stage.AssignmentLosses++
			addCommandLogDrainSample(stage, errSamples, fmt.Sprintf("%s/%s assignment lost",
				result.Partition.Kind, result.Partition.Key))
		}
		if result.ClaimLost {
			stage.ClaimLosses++
			addCommandLogDrainSample(stage, errSamples, fmt.Sprintf("%s/%s claim lost",
				result.Partition.Kind, result.Partition.Key))
		}
		if result.CommitFailure != "" {
			addCommandLogDrainSample(stage, errSamples, result.CommitFailure)
		}
		if result.FinalizerFailure != "" {
			addCommandLogDrainSample(stage, errSamples, fmt.Sprintf("%s/%s finalizer: %s",
				result.Partition.Kind, result.Partition.Key, result.FinalizerFailure))
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
	lister, ok := commandLog.(CommandPartitionOffsetLister)
	if !ok {
		return 0, fmt.Errorf("command log drain load: command log does not expose partition offsets")
	}
	offsets, err := lister.ListCommandPartitionOffsets(ctx, 0)
	if err != nil {
		return 0, err
	}
	var maxLag int64
	for _, offset := range offsets {
		lag := offset.TailOffset - offset.CommittedOffset
		if lag > maxLag {
			maxLag = lag
		}
	}
	return maxLag, nil
}

func mergeCommandLogLoadStage(dst, src CommandLogLoadStage) CommandLogLoadStage {
	dst.Commands += src.Commands
	dst.Succeeded += src.Succeeded
	dst.Failed += src.Failed
	dst.DurationMS += src.DurationMS
	dst.SampleErrorText = mergeLoadSamples(dst.SampleErrorText, src.SampleErrorText)
	if dst.DurationMS > 0 {
		dst.CommandsPerSec = float64(dst.Succeeded) / (float64(dst.DurationMS) / 1000)
	} else if src.CommandsPerSec > dst.CommandsPerSec {
		dst.CommandsPerSec = src.CommandsPerSec
	}
	return dst
}

func mergeCommandLogDrainStage(dst, src CommandLogDrainStage) CommandLogDrainStage {
	dst.Commands += src.Commands
	dst.Processed += src.Processed
	dst.Applied += src.Applied
	dst.TerminalFailures += src.TerminalFailures
	dst.RetryableFailures += src.RetryableFailures
	dst.CommitFailures += src.CommitFailures
	dst.AssignmentLosses += src.AssignmentLosses
	dst.ClaimLosses += src.ClaimLosses
	dst.Rounds += src.Rounds
	dst.DurationMS += src.DurationMS
	dst.SampleErrorText = mergeLoadSamples(dst.SampleErrorText, src.SampleErrorText)
	if dst.DurationMS > 0 {
		dst.CommandsPerSec = float64(dst.Applied) / (float64(dst.DurationMS) / 1000)
	} else if src.CommandsPerSec > dst.CommandsPerSec {
		dst.CommandsPerSec = src.CommandsPerSec
	}
	return dst
}

func mergeEventStoreProjectionLoadStage(dst, src EventStoreProjectionLoadStage) EventStoreProjectionLoadStage {
	dst.Enabled = dst.Enabled || src.Enabled
	if src.Partitions > dst.Partitions {
		dst.Partitions = src.Partitions
	}
	if src.PartitionLimit > dst.PartitionLimit {
		dst.PartitionLimit = src.PartitionLimit
	}
	dst.PartitionLimitExceeded = dst.PartitionLimitExceeded || src.PartitionLimitExceeded
	dst.AppliedEvents += src.AppliedEvents
	dst.Rounds += src.Rounds
	dst.DurationMS += src.DurationMS
	dst.SampleErrorText = mergeLoadSamples(dst.SampleErrorText, src.SampleErrorText)
	if dst.DurationMS > 0 {
		dst.EventsPerSec = float64(dst.AppliedEvents) / (float64(dst.DurationMS) / 1000)
	} else if src.EventsPerSec > dst.EventsPerSec {
		dst.EventsPerSec = src.EventsPerSec
	}
	return dst
}

func mergeLoadSamples(dst, src []string) []string {
	if len(src) == 0 || len(dst) >= 5 {
		return dst
	}
	seen := map[string]bool{}
	for _, text := range dst {
		seen[text] = true
	}
	for _, text := range src {
		if text == "" || seen[text] {
			continue
		}
		dst = append(dst, text)
		seen[text] = true
		if len(dst) >= 5 {
			break
		}
	}
	return dst
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

func addCommandLogDrainSample(stage *CommandLogDrainStage, seen map[string]bool, text string) {
	if stage == nil || text == "" || seen[text] || len(stage.SampleErrorText) >= 5 {
		return
	}
	stage.SampleErrorText = append(stage.SampleErrorText, text)
	seen[text] = true
}

func addEventProjectionLoadSample(stage *EventStoreProjectionLoadStage, text string) {
	if stage == nil || text == "" || len(stage.SampleErrorText) >= 5 {
		return
	}
	for _, existing := range stage.SampleErrorText {
		if existing == text {
			return
		}
	}
	stage.SampleErrorText = append(stage.SampleErrorText, text)
}
