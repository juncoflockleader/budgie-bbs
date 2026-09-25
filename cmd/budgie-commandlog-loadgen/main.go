package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/loadtest"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
	"github.com/juncoflockleader/budgie-bbs/internal/scalebudget"
)

func main() {
	os.Exit(run())
}

func defaultCommandLogLoadStream(prefix string) string {
	return fmt.Sprintf("%s%d_%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func defaultCommandLogLoadIndexPrefix() string {
	return fmt.Sprintf("budgie:commandlog-load:%d:%d", os.Getpid(), time.Now().UnixNano())
}

func run() int {
	defaults := loadmodel.DefaultCommandLogDrainLoadConfig()
	var (
		postgresDSN           = flag.String("postgres-dsn", runconfig.EnvOr("BUDGIE_POSTGRES_DSN", ""), "Optional PostgreSQL DSN for SQL-backed writer materialization; SQLite temp DB is used when empty")
		sqlitePath            = flag.String("sqlite-path", "", "SQLite path for the synthetic run; defaults to a temporary file when -postgres-dsn is empty")
		requirePostgres       = flag.Bool("require-postgres", false, "Fail if -postgres-dsn or BUDGIE_POSTGRES_DSN is empty; use for staging promotion gates")
		schema                = flag.String("schema", "", "Postgres schema to create for the load run; defaults to a unique temporary schema")
		keepSchema            = flag.Bool("keep-schema", false, "Keep the Postgres load schema after the run")
		boards                = flag.Int("boards", defaults.Boards, "Number of board command partitions")
		commandsPerBoard      = flag.Int("commands-per-board", defaults.CommandsPerBoard, "Number of createThread commands to enqueue per board partition")
		repliesPerThread      = flag.Int("replies-per-thread", defaults.RepliesPerThread, "Number of appendPost commands to enqueue for each projected load thread after the createThread drain")
		directedReplies       = flag.Bool("directed-replies", defaults.DirectedReplies, "Make synthetic appendPost commands reply to each thread's root post; requires -replies-per-thread > 0")
		submitConcurrency     = flag.Int("submit-concurrency", defaults.SubmitConcurrency, "Maximum concurrent command-log producers")
		writers               = flag.Int("writers", defaults.Writers, "Synthetic command-log writer count")
		batchSize             = flag.Int("batch-size", defaults.BatchSize, "Commands fetched per partition by each writer drain round")
		partitionConcurrency  = flag.Int("partition-concurrency", defaults.PartitionConcurrency, "Maximum command partitions each synthetic writer drains concurrently")
		bodyBytes             = flag.Int("body-bytes", defaults.BodyBytes, "Post body size in bytes")
		boardPrefix           = flag.String("board-prefix", defaults.BoardPrefix, "Board id prefix for generated load boards")
		userName              = flag.String("user", defaults.UserName, "Load user name")
		assignmentMode        = flag.String("assignment-mode", defaults.AssignmentMode, "Synthetic writer ownership mode: hash-assignment or snapshot-assignment")
		authoritative         = flag.Bool("authoritative-submit", false, "Submit measured commands through Core authoritative command-log enqueue path before writer drain")
		workerExecutor        = flag.String("command-log-worker-executor", defaults.ExecutorMode, "Command-log writer executor for the synthetic run: sql or native")
		commandLogBackend     = flag.String("command-log-backend", "memory", "Command log backend for the synthetic run: memory, nats, or kafka/redpanda")
		natsURL               = flag.String("nats", runconfig.EnvOr("BUDGIE_NATS_URL", ""), "NATS URL for -command-log-backend nats")
		natsStream            = flag.String("command-log-nats-stream", defaultCommandLogLoadStream(loadmodel.CommandLogLoadCommandNATSStreamPrefix), "NATS JetStream stream for -command-log-backend nats")
		natsReplicas          = flag.Int("command-log-nats-replicas", 1, "NATS JetStream replica count for -command-log-backend nats")
		eventNATSStream       = flag.String("event-log-nats-stream", defaultCommandLogLoadStream(loadmodel.CommandLogLoadEventNATSStreamPrefix), "NATS JetStream event-log stream for -command-log-worker-executor native")
		eventNATSReplicas     = flag.Int("event-log-nats-replicas", 1, "NATS JetStream event-log replica count for -command-log-worker-executor native")
		commandLogIndex       = flag.String("command-log-index", runconfig.EnvOr("BUDGIE_COMMAND_LOG_INDEX", ""), "Optional command-log partition index backend: redis")
		commandLogIndexPrefix = flag.String("command-log-index-prefix", runconfig.EnvOr("BUDGIE_COMMAND_LOG_INDEX_PREFIX", defaultCommandLogLoadIndexPrefix()), "Redis key prefix for -command-log-index redis")
		redisURL              = flag.String("redis", runconfig.EnvOr("BUDGIE_REDIS_URL", ""), "Redis URL for -command-log-index redis")
		kafkaBrokers          = flag.String("kafka-brokers", runconfig.EnvOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers for -command-log-backend kafka/redpanda")
		kafkaCommandTopic     = flag.String("kafka-command-topic", kafkaconn.DefaultCommandTopic, "Kafka/Redpanda command-log topic")
		kafkaEventTopic       = flag.String("kafka-event-topic", kafkaconn.DefaultEventTopic, "Kafka/Redpanda event-log topic for native command/event runs")
		kafkaConsumerGroup    = flag.String("kafka-consumer-group", kafkaconn.DefaultWriterConsumerGroup, "Kafka/Redpanda writer consumer group")
		kafkaScalarAllocator  = flag.String("kafka-scalar-allocator", runconfig.EnvOr("BUDGIE_KAFKA_SCALAR_ALLOCATOR", ""), "Kafka/Redpanda scalar compatibility allocator: sql-event-scalar-offsets or sql-event-partition-offsets")
		kafkaPartitions       = flag.Int("kafka-command-partitions", 0, "Kafka/Redpanda command-topic partition count for logical-partition mapping")
		kafkaEventPartitions  = flag.Int("kafka-event-partitions", 0, "Kafka/Redpanda event-topic partition count for logical event-partition mapping")
		kafkaTopicReplicas    = flag.Int("kafka-topic-replicas", 1, "Kafka/Redpanda replication factor to request when creating load topics")
		kafkaSecurityFlags    = kafkaconn.RegisterRuntimeSecurityFlags(flag.CommandLine)
		timeout               = flag.Duration("timeout", 2*time.Minute, "Maximum duration for the load run")
		budgetFile            = flag.String("budget-file", "", "Path to JSON internet-scale budget file; commandLogDrain section enforces additional thresholds")
		minDrainCommandsPS    = flag.Float64("min-drain-commands-per-sec", 0, "Fail if writer drain throughput is below this value; 0 reports only")
		maxLagAfterDrain      = flag.Int64("max-lag-after-drain", 0, "Fail if max command partition lag after drain exceeds this value")
		pretty                = flag.Bool("pretty", true, "Pretty-print JSON output")
	)
	flag.Parse()

	budgets, err := scalebudget.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}

	ctx, cancel := runconfig.InterruptTimeoutContext(context.Background(), *timeout)
	defer cancel()

	executorMode := loadmodel.NormalizeCommandLogExecutor(*workerExecutor)
	if !loadmodel.IsSupportedCommandLogDrainExecutorMode(executorMode) {
		log.Printf("unsupported command-log worker executor %q; supported: %s", *workerExecutor, runconfig.SupportedCommandLogExecutors())
		return 2
	}
	runtimeConfig := loadtest.CommandLogLoadRuntimeConfig{
		PostgresDSN:           *postgresDSN,
		RequirePostgres:       *requirePostgres,
		PostgresSchema:        runconfig.MaybeDisposablePostgresSchema(*postgresDSN, *schema, loadmodel.CommandLogLoadPostgresSchemaPrefix),
		KeepPostgresSchema:    *keepSchema,
		NATSURL:               *natsURL,
		Backend:               *commandLogBackend,
		ExecutorMode:          executorMode,
		CommandNATSStream:     *natsStream,
		CommandNATSReplicas:   *natsReplicas,
		EventNATSStream:       *eventNATSStream,
		EventNATSReplicas:     *eventNATSReplicas,
		CommandLogIndex:       *commandLogIndex,
		CommandLogIndexPrefix: *commandLogIndexPrefix,
		RedisURL:              *redisURL,
		ScalarAllocator:       *kafkaScalarAllocator,
		Kafka:                 kafkaconn.RuntimeConfigFromOptions(*kafkaBrokers, *kafkaCommandTopic, *kafkaEventTopic, *kafkaConsumerGroup, kafkaSecurityFlags.Config()),
		KafkaPartitions:       int32(*kafkaPartitions),
		KafkaEventPartitions:  int32(*kafkaEventPartitions),
		KafkaTopicReplicas:    *kafkaTopicReplicas,
	}
	if err := loadtest.ValidateCommandLogLoadRuntimeConfig(runtimeConfig); err != nil {
		log.Print(err)
		return 2
	}
	clientID := fmt.Sprintf("budgie-commandlog-loadgen-%d", os.Getpid())
	if loadtest.NormalizeCommandLogLoadBackend(runtimeConfig.Backend) == "kafka" {
		var specs []kafkaconn.TopicProvisioningSpec
		if executorMode == loadmodel.CommandLogDrainExecutorNative {
			specs, err = kafkaconn.CommandEventTopicProvisioningSpecs(
				runtimeConfig.Kafka,
				runtimeConfig.KafkaPartitions,
				runtimeConfig.KafkaEventPartitions,
				runtimeConfig.KafkaTopicReplicas,
			)
		} else {
			specs, err = kafkaconn.CommandTopicProvisioningSpecs(
				runtimeConfig.Kafka,
				runtimeConfig.KafkaPartitions,
				runtimeConfig.KafkaTopicReplicas,
			)
		}
		if err != nil {
			err = fmt.Errorf("kafka command-log load requires -kafka-topic-replicas: %w", err)
		}
		if err == nil {
			err = kafkaconn.EnsureTopics(ctx, kafkaconn.TopicProvisioningOptions{
				Runtime:  runtimeConfig.Kafka,
				ClientID: clientID + "-topic-setup",
				Topics:   specs,
			})
		}
		if err != nil {
			log.Printf("ensure Kafka load topics: %v", err)
			return 2
		}
	}
	var (
		commandLog         core.CommandLog
		commandLogCleanup  func()
		nativeTransactions core.CommandEventTransactionStore
		nativeEventStore   core.EventStore
		nativeStoreBinder  loadtest.NativeCommandEventStoreBinder
		commandLogOpenErr  error
	)
	if executorMode == loadmodel.CommandLogDrainExecutorNative {
		var stores loadtest.NativeCommandEventStores
		stores, commandLogOpenErr = loadtest.OpenNativeCommandEventStores(ctx, loadtest.NativeCommandEventStoreConfig{
			Backend:                *commandLogBackend,
			NATSURL:                *natsURL,
			CommandNATSStream:      *natsStream,
			CommandNATSReplicas:    *natsReplicas,
			EventNATSStream:        *eventNATSStream,
			EventNATSReplicas:      *eventNATSReplicas,
			Kafka:                  runtimeConfig.Kafka,
			KafkaCommandPartitions: runtimeConfig.KafkaPartitions,
			KafkaEventPartitions:   runtimeConfig.KafkaEventPartitions,
			ScalarAllocator:        runtimeConfig.ScalarAllocator,
			ClientID:               clientID,
		})
		commandLog = stores.CommandLog
		nativeTransactions = stores.Transactions
		nativeEventStore = stores.EventStore
		nativeStoreBinder = stores.Binder
		commandLogCleanup = stores.Cleanup
	} else {
		commandLog, commandLogCleanup, commandLogOpenErr = loadtest.OpenCommandLog(ctx, loadtest.CommandLogOpenConfig{
			Backend:         *commandLogBackend,
			NATSURL:         *natsURL,
			NATSStream:      *natsStream,
			NATSReplicas:    *natsReplicas,
			Authoritative:   *authoritative,
			Kafka:           runtimeConfig.Kafka,
			KafkaPartitions: runtimeConfig.KafkaPartitions,
			ClientID:        clientID,
		})
	}
	if commandLogOpenErr != nil {
		log.Printf("open command log: %v", commandLogOpenErr)
		return 2
	}
	defer commandLogCleanup()
	if indexBackend := loadtest.NormalizeCommandLogLoadIndexBackend(*commandLogIndex); indexBackend != "" {
		indexedLog, indexCleanup, err := loadtest.OpenCommandLogLoadIndex(ctx, commandLog, loadtest.CommandLogIndexOpenConfig{
			Backend:  indexBackend,
			RedisURL: *redisURL,
			Prefix:   *commandLogIndexPrefix,
			Logf:     log.Printf,
		})
		if err != nil {
			log.Printf("open command log index: %v", err)
			return 2
		}
		defer indexCleanup()
		commandLog = indexedLog
	}

	var authoritativeCommandLog core.CommandLog
	if *authoritative {
		authoritativeCommandLog = commandLog
	}
	coreOptions := []core.Option{}
	if authoritativeCommandLog != nil {
		coreOptions = append(coreOptions, core.WithAuthoritativeCommandLog(authoritativeCommandLog))
	}
	c, cleanup, err := loadtest.OpenCore(ctx, loadtest.CoreConfig{
		PostgresDSN:       *postgresDSN,
		SQLitePath:        *sqlitePath,
		Schema:            runtimeConfig.PostgresSchema,
		SchemaPrefix:      loadmodel.CommandLogLoadPostgresSchemaPrefix,
		KeepSchema:        *keepSchema,
		Logf:              log.Printf,
		SQLiteTempPattern: "budgie-commandlog-load-*.db",
	}, coreOptions...)
	if err != nil {
		log.Printf("open load core: %v", err)
		return 1
	}
	defer cleanup()
	defer c.DB.Close()
	if nativeStoreBinder != nil {
		nativeTransactions, nativeEventStore, err = nativeStoreBinder(c.DB)
		if err != nil {
			log.Printf("open native command/event stores: %v", err)
			return 2
		}
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go c.Run(runCtx)

	config := loadmodel.CommandLogDrainLoadConfig{
		Boards:               *boards,
		CommandsPerBoard:     *commandsPerBoard,
		RepliesPerThread:     *repliesPerThread,
		DirectedReplies:      *directedReplies,
		SubmitConcurrency:    *submitConcurrency,
		Writers:              *writers,
		BatchSize:            *batchSize,
		PartitionConcurrency: *partitionConcurrency,
		BodyBytes:            *bodyBytes,
		BoardPrefix:          *boardPrefix,
		UserName:             *userName,
		AssignmentMode:       *assignmentMode,
		ExecutorMode:         executorMode,
		AuthoritativeSubmit:  *authoritative,
	}
	var report loadmodel.CommandLogDrainLoadReport
	if executorMode == loadmodel.CommandLogDrainExecutorNative {
		report, err = c.RunNativeCommandEventProjectionLoadWithStores(ctx, config, commandLog, nativeTransactions, nativeEventStore)
	} else if *authoritative {
		report, err = c.RunAuthoritativeCommandLogDrainLoad(ctx, config)
	} else if commandLog != nil {
		report, err = c.RunCommandLogDrainLoadWithCommandLog(ctx, config, commandLog)
	} else {
		report, err = c.RunCommandLogDrainLoad(ctx, config)
	}
	report.Runtime = loadtest.CommandLogLoadRuntimeReport(runtimeConfig)
	report.Evidence = runevidence.CollectForTool("budgie-commandlog-loadgen", *budgetFile)
	if err := loadtest.AttachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
		log.Printf("scalar compatibility audit: %v", err)
		return 1
	}
	if printErr := runreport.WriteJSON(os.Stdout, report, *pretty); printErr != nil {
		log.Printf("print report: %v", printErr)
		return 1
	}
	if err != nil {
		log.Printf("command-log load run failed: %v", err)
		return 1
	}
	if *minDrainCommandsPS > 0 && report.Drain.CommandsPerSec < *minDrainCommandsPS {
		log.Printf("drain commands/sec %.2f below threshold %.2f", report.Drain.CommandsPerSec, *minDrainCommandsPS)
		return 3
	}
	if report.MaxPartitionLagAfterDrain > *maxLagAfterDrain {
		log.Printf("max lag after drain %d above threshold %d", report.MaxPartitionLagAfterDrain, *maxLagAfterDrain)
		return 3
	}
	if violations := scalebudget.EvaluateCommandLogDrainBudget(report, budgets.CommandLogDrain); len(violations) > 0 {
		log.Printf("scale budget violations: %s", scalebudget.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}
