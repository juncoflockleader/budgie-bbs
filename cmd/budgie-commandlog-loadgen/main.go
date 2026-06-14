package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
	"github.com/juncoflockleader/budgie-bbs/internal/redisconn"
)

var schemaNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := core.DefaultCommandLogDrainLoadConfig()
	kafkaSecurityDefaults := kafkaconn.RuntimeSecurityConfigFromEnv()
	var (
		postgresDSN           = flag.String("postgres-dsn", envOr("BUDGIE_POSTGRES_DSN", ""), "Optional PostgreSQL DSN for SQL-backed writer materialization; SQLite temp DB is used when empty")
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
		natsURL               = flag.String("nats", envOr("BUDGIE_NATS_URL", ""), "NATS URL for -command-log-backend nats")
		natsStream            = flag.String("command-log-nats-stream", defaultCommandLogLoadNATSStream(), "NATS JetStream stream for -command-log-backend nats")
		natsReplicas          = flag.Int("command-log-nats-replicas", 1, "NATS JetStream replica count for -command-log-backend nats")
		eventNATSStream       = flag.String("event-log-nats-stream", defaultEventLogLoadNATSStream(), "NATS JetStream event-log stream for -command-log-worker-executor native")
		eventNATSReplicas     = flag.Int("event-log-nats-replicas", 1, "NATS JetStream event-log replica count for -command-log-worker-executor native")
		commandLogIndex       = flag.String("command-log-index", envOr("BUDGIE_COMMAND_LOG_INDEX", ""), "Optional command-log partition index backend: redis")
		commandLogIndexPrefix = flag.String("command-log-index-prefix", envOr("BUDGIE_COMMAND_LOG_INDEX_PREFIX", defaultCommandLogLoadIndexPrefix()), "Redis key prefix for -command-log-index redis")
		redisURL              = flag.String("redis", envOr("BUDGIE_REDIS_URL", ""), "Redis URL for -command-log-index redis")
		kafkaBrokers          = flag.String("kafka-brokers", envOr("BUDGIE_KAFKA_BROKERS", ""), "Comma-separated Kafka/Redpanda brokers for -command-log-backend kafka/redpanda")
		kafkaCommandTopic     = flag.String("kafka-command-topic", kafkaconn.DefaultCommandTopic, "Kafka/Redpanda command-log topic")
		kafkaEventTopic       = flag.String("kafka-event-topic", kafkaconn.DefaultEventTopic, "Kafka/Redpanda event-log topic for native command/event runs")
		kafkaConsumerGroup    = flag.String("kafka-consumer-group", kafkaconn.DefaultWriterConsumerGroup, "Kafka/Redpanda writer consumer group")
		kafkaScalarAllocator  = flag.String("kafka-scalar-allocator", envOr("BUDGIE_KAFKA_SCALAR_ALLOCATOR", ""), "Kafka/Redpanda scalar compatibility allocator: sql-event-scalar-offsets or sql-event-partition-offsets")
		kafkaPartitions       = flag.Int("kafka-command-partitions", 0, "Kafka/Redpanda command-topic partition count for logical-partition mapping")
		kafkaEventPartitions  = flag.Int("kafka-event-partitions", 0, "Kafka/Redpanda event-topic partition count for logical event-partition mapping")
		kafkaTopicReplicas    = flag.Int("kafka-topic-replicas", 1, "Kafka/Redpanda replication factor to request when creating load topics")
		kafkaTLS              = flag.Bool("kafka-tls", kafkaSecurityDefaults.TLS, "Enable TLS for Kafka/Redpanda connections (also read from BUDGIE_KAFKA_TLS)")
		kafkaTLSCAFile        = flag.String("kafka-tls-ca-file", kafkaSecurityDefaults.TLSCAFile, "Optional PEM CA bundle for Kafka/Redpanda TLS (also read from BUDGIE_KAFKA_TLS_CA_FILE)")
		kafkaTLSServerName    = flag.String("kafka-tls-server-name", kafkaSecurityDefaults.TLSServerName, "Optional TLS server name override for Kafka/Redpanda (also read from BUDGIE_KAFKA_TLS_SERVER_NAME)")
		kafkaSASLMechanism    = flag.String("kafka-sasl-mechanism", kafkaSecurityDefaults.SASLMechanism, "Kafka/Redpanda SASL mechanism: plain, scram-sha-256, or scram-sha-512 (also read from BUDGIE_KAFKA_SASL_MECHANISM)")
		kafkaSASLUser         = flag.String("kafka-sasl-user", kafkaSecurityDefaults.SASLUser, "Kafka/Redpanda SASL user (also read from BUDGIE_KAFKA_SASL_USER)")
		kafkaSASLPassword     = flag.String("kafka-sasl-password", kafkaSecurityDefaults.SASLPassword, "Kafka/Redpanda SASL password (also read from BUDGIE_KAFKA_SASL_PASSWORD)")
		timeout               = flag.Duration("timeout", 2*time.Minute, "Maximum duration for the load run")
		budgetFile            = flag.String("budget-file", "", "Path to JSON internet-scale budget file; commandLogDrain section enforces additional thresholds")
		minDrainCommandsPS    = flag.Float64("min-drain-commands-per-sec", 0, "Fail if writer drain throughput is below this value; 0 reports only")
		maxLagAfterDrain      = flag.Int64("max-lag-after-drain", 0, "Fail if max command partition lag after drain exceeds this value")
		pretty                = flag.Bool("pretty", true, "Pretty-print JSON output")
	)
	flag.Parse()

	budgets, err := core.LoadScaleBudgets(*budgetFile)
	if err != nil {
		log.Printf("load budget file: %v", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	executorMode := normalizeCommandLogLoadExecutorMode(*workerExecutor)
	if !isSupportedCommandLogLoadExecutorMode(executorMode) {
		log.Printf("unsupported command-log worker executor %q; supported: sql,native", *workerExecutor)
		return 2
	}
	runtimeConfig := commandLogLoadRuntimeConfig{
		PostgresDSN:           *postgresDSN,
		RequirePostgres:       *requirePostgres,
		PostgresSchema:        commandLogLoadPostgresSchema(*postgresDSN, *schema),
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
		Kafka: kafkaconn.RuntimeConfigFromOptions(*kafkaBrokers, *kafkaCommandTopic, *kafkaEventTopic, *kafkaConsumerGroup, kafkaconn.RuntimeSecurityConfig{
			TLS:           *kafkaTLS,
			TLSCAFile:     *kafkaTLSCAFile,
			TLSServerName: *kafkaTLSServerName,
			SASLMechanism: *kafkaSASLMechanism,
			SASLUser:      *kafkaSASLUser,
			SASLPassword:  *kafkaSASLPassword,
		}),
		KafkaPartitions:      int32(*kafkaPartitions),
		KafkaEventPartitions: int32(*kafkaEventPartitions),
		KafkaTopicReplicas:   *kafkaTopicReplicas,
	}
	if err := validateCommandLogLoadRuntimeConfig(runtimeConfig); err != nil {
		log.Print(err)
		return 2
	}
	if err := ensureCommandLogLoadKafkaTopics(ctx, runtimeConfig); err != nil {
		log.Printf("ensure Kafka load topics: %v", err)
		return 2
	}
	var (
		commandLog         core.CommandLog
		commandLogCleanup  func()
		nativeTransactions core.CommandEventTransactionStore
		nativeEventStore   core.EventStore
		nativeStoreBinder  nativeCommandEventStoreBinder
		commandLogOpenErr  error
	)
	if executorMode == core.CommandLogDrainExecutorNative {
		commandLog, nativeTransactions, nativeEventStore, nativeStoreBinder, commandLogCleanup, commandLogOpenErr = openNativeCommandEventStores(ctx, *commandLogBackend, *natsURL, *natsStream, *natsReplicas, *eventNATSStream, *eventNATSReplicas, runtimeConfig.Kafka, runtimeConfig.KafkaPartitions, runtimeConfig.KafkaEventPartitions, runtimeConfig.ScalarAllocator)
	} else {
		commandLog, commandLogCleanup, commandLogOpenErr = openCommandLog(ctx, *commandLogBackend, *natsURL, *natsStream, *natsReplicas, *authoritative, runtimeConfig.Kafka, runtimeConfig.KafkaPartitions)
	}
	if commandLogOpenErr != nil {
		log.Printf("open command log: %v", commandLogOpenErr)
		return 2
	}
	defer commandLogCleanup()
	if indexBackend := normalizeCommandLogLoadIndexBackend(*commandLogIndex); indexBackend != "" {
		indexedLog, indexCleanup, err := openCommandLogLoadIndex(ctx, commandLog, indexBackend, *redisURL, *commandLogIndexPrefix)
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
	c, cleanup, err := openCore(ctx, *postgresDSN, *sqlitePath, runtimeConfig.PostgresSchema, *keepSchema, authoritativeCommandLog)
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

	config := core.CommandLogDrainLoadConfig{
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
	var report core.CommandLogDrainLoadReport
	if executorMode == core.CommandLogDrainExecutorNative {
		report, err = c.RunNativeCommandEventProjectionLoadWithStores(ctx, config, commandLog, nativeTransactions, nativeEventStore)
	} else if *authoritative {
		report, err = c.RunAuthoritativeCommandLogDrainLoad(ctx, config)
	} else if commandLog != nil {
		report, err = c.RunCommandLogDrainLoadWithCommandLog(ctx, config, commandLog)
	} else {
		report, err = c.RunCommandLogDrainLoad(ctx, config)
	}
	report.Runtime = commandLogLoadRuntimeReport(runtimeConfig)
	report.Evidence = commandLogLoadEvidence(*budgetFile)
	if err := attachCommandLogLoadScalarCompatibilityAudit(ctx, c.DB, &report); err != nil {
		log.Printf("scalar compatibility audit: %v", err)
		return 1
	}
	if printErr := printReport(report, *pretty); printErr != nil {
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
	if violations := core.EvaluateCommandLogDrainBudget(report, budgets.CommandLogDrain); len(violations) > 0 {
		log.Printf("scale budget violations: %s", core.FormatScaleBudgetViolations(violations))
		return 3
	}
	return 0
}

func openCommandLog(ctx context.Context, backend, natsURL, stream string, replicas int, authoritative bool, kafkaConfig kafkaconn.RuntimeConfig, kafkaPartitions int32) (core.CommandLog, func(), error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	switch backend {
	case "", "memory":
		if authoritative {
			return core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient()), func() {}, nil
		}
		return nil, func() {}, nil
	case "nats", "jetstream":
		natsURL = strings.TrimSpace(natsURL)
		if natsURL == "" {
			return nil, func() {}, fmt.Errorf("-command-log-backend nats requires -nats or BUDGIE_NATS_URL")
		}
		conn, err := natsconn.Dial(natsURL)
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			conn.Close()
		}
		log, err := natsconn.NewJetStreamCommandLog(ctx, conn, natsconn.JetStreamCommandLogOptions{
			Stream:   stream,
			Replicas: replicas,
		})
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		return log, cleanup, nil
	case "kafka", "redpanda":
		if err := kafkaConfig.ValidateCommandLog(); err != nil {
			return nil, func() {}, err
		}
		if kafkaPartitions <= 0 {
			return nil, func() {}, fmt.Errorf("command log backend %q requires -kafka-command-partitions for logical partition mapping", backend)
		}
		runtime := kafkaConfig.Normalize()
		client, err := kafkaconn.NewCommandLogRuntimeClient(ctx, kafkaconn.CommandLogRuntimeClientOptions{
			Runtime:  runtime,
			ClientID: fmt.Sprintf("budgie-commandlog-loadgen-%d", os.Getpid()),
		})
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			client.CloseAllowingRebalance()
		}
		log := kafkaconn.NewCommandLog(
			kafkaconn.NewFranzCommandLogClient(client, kafkaconn.FranzCommandLogClientOptions{}),
			kafkaconn.CommandLogOptions{
				CommandTopic:   runtime.CommandTopic,
				ConsumerGroup:  runtime.ConsumerGroup,
				PartitionCount: kafkaPartitions,
			},
		)
		return newIndexedCommandLog(log), cleanup, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported command log backend %q; supported: memory,nats,kafka", backend)
	}
}

func openCommandLogLoadIndex(ctx context.Context, commandLog core.CommandLog, backend, redisURL, prefix string) (core.CommandLog, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	backend = normalizeCommandLogLoadIndexBackend(backend)
	switch backend {
	case "":
		return commandLog, func() {}, nil
	case "redis":
		if commandLog == nil {
			return nil, func() {}, fmt.Errorf("-command-log-index redis requires an explicit command log backend; memory direct mode has no external command log")
		}
		client, err := redisconn.NewClient(redisURL)
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			if err := client.Close(); err != nil {
				log.Printf("close Redis command-log index: %v", err)
			}
		}
		index := redisconn.NewCommandLogPartitionIndex(client, redisconn.CommandLogPartitionIndexOptions{
			Prefix: prefix,
		})
		return core.NewIndexedCommandLog(commandLog, index), cleanup, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported command-log index backend %q; supported: redis", backend)
	}
}

type nativeCommandEventStoreBinder func(*sql.DB) (core.CommandEventTransactionStore, core.EventStore, error)

func openNativeCommandEventStores(ctx context.Context, backend, natsURL, commandStream string, commandReplicas int, eventStream string, eventReplicas int, kafkaConfig kafkaconn.RuntimeConfig, kafkaCommandPartitions, kafkaEventPartitions int32, scalarAllocator string) (core.CommandLog, core.CommandEventTransactionStore, core.EventStore, nativeCommandEventStoreBinder, func(), error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	switch backend {
	case "", "memory":
		commandClient := core.NewMemoryBrokerCommandLogClient()
		eventClient := core.NewMemoryBrokerEventLogClient()
		return core.NewBrokerCommandLog(commandClient),
			core.NewBrokerCommandEventTransactionStore(core.NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient)),
			core.NewBrokerEventStore(eventClient),
			nil,
			func() {},
			nil
	case "nats", "jetstream":
		natsURL = strings.TrimSpace(natsURL)
		if natsURL == "" {
			return nil, nil, nil, nil, func() {}, fmt.Errorf("-command-log-backend nats requires -nats or BUDGIE_NATS_URL")
		}
		conn, err := natsconn.Dial(natsURL)
		if err != nil {
			return nil, nil, nil, nil, func() {}, err
		}
		cleanup := func() {
			conn.Close()
		}
		commandClient, err := natsconn.NewJetStreamCommandLogClient(ctx, conn, natsconn.JetStreamCommandLogOptions{
			Stream:   commandStream,
			Replicas: commandReplicas,
		})
		if err != nil {
			cleanup()
			return nil, nil, nil, nil, func() {}, err
		}
		eventClient, err := natsconn.NewJetStreamEventLogClient(ctx, conn, natsconn.JetStreamEventLogOptions{
			Stream:   eventStream,
			Replicas: eventReplicas,
		})
		if err != nil {
			cleanup()
			return nil, nil, nil, nil, func() {}, err
		}
		commandLog := core.NewBrokerCommandLog(commandClient)
		transactions := core.NewBrokerCommandEventTransactionStore(
			natsconn.NewJetStreamCommandEventTransactionClientFromClients(commandClient, eventClient),
		)
		eventStore := core.NewBrokerEventStore(eventClient)
		return commandLog, transactions, eventStore, nil, cleanup, nil
	case "kafka", "redpanda":
		if err := kafkaConfig.ValidateCommandEventTransaction(); err != nil {
			return nil, nil, nil, nil, func() {}, err
		}
		return openNativeKafkaCommandEventStores(ctx, kafkaConfig, kafkaCommandPartitions, kafkaEventPartitions, scalarAllocator)
	default:
		return nil, nil, nil, nil, func() {}, fmt.Errorf("unsupported command log backend %q; supported: memory,nats,kafka", backend)
	}
}

func openNativeKafkaCommandEventStores(ctx context.Context, kafkaConfig kafkaconn.RuntimeConfig, commandPartitions, eventPartitions int32, scalarAllocator string) (core.CommandLog, core.CommandEventTransactionStore, core.EventStore, nativeCommandEventStoreBinder, func(), error) {
	if commandPartitions <= 0 {
		return nil, nil, nil, nil, func() {}, fmt.Errorf("native Kafka command/event load requires -kafka-command-partitions for logical command-partition mapping")
	}
	if eventPartitions <= 0 {
		return nil, nil, nil, nil, func() {}, fmt.Errorf("native Kafka command/event load requires -kafka-event-partitions for logical event-partition mapping")
	}
	runtime := kafkaConfig.Normalize()
	clientID := fmt.Sprintf("budgie-commandlog-loadgen-%d", os.Getpid())
	commandProducerClient, err := kafkaconn.NewCommandLogProducerRuntimeClient(ctx, kafkaconn.CommandLogProducerRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID + "-command-producer",
	})
	if err != nil {
		return nil, nil, nil, nil, func() {}, err
	}
	var eventClientCleanup func()
	var transactionSessionCleanup func()
	cleanup := func() {
		if eventClientCleanup != nil {
			eventClientCleanup()
		}
		if transactionSessionCleanup != nil {
			transactionSessionCleanup()
		}
		commandProducerClient.Close()
	}
	commandProducerLog := kafkaconn.NewCommandLog(
		kafkaconn.NewFranzCommandLogClient(commandProducerClient, kafkaconn.FranzCommandLogClientOptions{}),
		kafkaconn.CommandLogOptions{
			CommandTopic:   runtime.CommandTopic,
			ConsumerGroup:  runtime.ConsumerGroup,
			PartitionCount: commandPartitions,
		},
	)
	commandLog := core.NewSwitchableCommandLog(commandProducerLog)
	binder := func(db *sql.DB) (core.CommandEventTransactionStore, core.EventStore, error) {
		if db == nil {
			return nil, nil, fmt.Errorf("native Kafka command/event load requires a materialization database")
		}
		partitionOnly := commandLogLoadScalarCompatibilityAllocator("kafka", core.CommandLogDrainExecutorNative, scalarAllocator) == core.CommandLogDrainScalarAllocatorSQLEventPartitions
		allocator := kafkaconn.NewSQLEventPositionAllocator(db, kafkaconn.SQLEventPositionAllocatorOptions{
			DisableCompatibilitySeq: partitionOnly,
		})
		transactionSession, err := kafkaconn.NewCommandWriterTransactionSession(ctx, kafkaconn.CommandWriterClientOptions{
			Runtime:         runtime,
			ClientID:        clientID + "-writer",
			TransactionalID: clientID + "-tx",
		})
		if err != nil {
			return nil, nil, err
		}
		transactionSessionCleanup = transactionSession.CloseAllowingRebalance
		commandLog.SetDrainLog(kafkaconn.NewCommandLog(
			kafkaconn.NewFranzCommandLogClient(transactionSession.Client(), kafkaconn.FastDrainFranzCommandLogClientOptions()),
			kafkaconn.CommandLogOptions{
				CommandTopic:   runtime.CommandTopic,
				ConsumerGroup:  runtime.ConsumerGroup,
				PartitionCount: commandPartitions,
			},
		))
		transactions := core.NewBrokerCommandEventTransactionStoreWithOptions(
			kafkaconn.NewCommandEventTransactionClient(
				kafkaconn.NewFranzCommandEventTransactionBeginner(transactionSession, allocator),
				kafkaconn.CommandEventTransactionOptions{
					CommandTopic:             runtime.CommandTopic,
					EventTopic:               runtime.EventTopic,
					ConsumerGroup:            runtime.ConsumerGroup,
					AllowPartitionOnlyEvents: partitionOnly,
				},
			),
			core.BrokerCommandEventTransactionStoreOptions{AllowPartitionOnlyEvents: partitionOnly},
		)
		eventClient, err := kafkaconn.NewEventLogRuntimeClient(ctx, kafkaconn.EventLogRuntimeClientOptions{
			Runtime:  runtime,
			ClientID: fmt.Sprintf("budgie-commandlog-loadgen-%d-events", os.Getpid()),
		})
		if err != nil {
			return nil, nil, err
		}
		eventClientCleanup = func() {
			eventClient.Close()
		}
		eventLogOptions := kafkaconn.EventLogOptions{
			EventTopic:                  runtime.EventTopic,
			PartitionCount:              eventPartitions,
			Partitions:                  allocator,
			DisableKafkaOffsetStreamSeq: partitionOnly,
		}
		if !partitionOnly {
			eventLogOptions.Head = allocator
		}
		eventStore := kafkaconn.NewEventStore(
			kafkaconn.NewFranzEventLogClient(eventClient, kafkaconn.FranzEventLogClientOptions{}),
			eventLogOptions,
		)
		return transactions, eventStore, nil
	}
	return newIndexedCommandLog(commandLog), nil, nil, binder, cleanup, nil
}

func openCore(ctx context.Context, postgresDSN, sqlitePath, schema string, keepSchema bool, authoritativeCommandLog core.CommandLog) (*core.Core, func(), error) {
	postgresDSN = strings.TrimSpace(postgresDSN)
	options := []core.Option{}
	if authoritativeCommandLog != nil {
		options = append(options, core.WithAuthoritativeCommandLog(authoritativeCommandLog))
	}
	if postgresDSN == "" {
		path := strings.TrimSpace(sqlitePath)
		tempPath := false
		if path == "" {
			f, err := os.CreateTemp("", "budgie-commandlog-load-*.db")
			if err != nil {
				return nil, func() {}, err
			}
			path = f.Name()
			if err := f.Close(); err != nil {
				_ = os.Remove(path)
				return nil, func() {}, err
			}
			tempPath = true
		}
		c, err := core.New(path, options...)
		if err != nil {
			if tempPath {
				_ = os.Remove(path)
			}
			return nil, func() {}, err
		}
		return c, func() {
			if tempPath {
				_ = os.Remove(path)
			}
		}, nil
	}

	schemaName := strings.TrimSpace(schema)
	if schemaName == "" {
		schemaName = fmt.Sprintf("budgie_cmdlog_load_%d_%d", os.Getpid(), time.Now().UnixNano())
	}
	if !schemaNamePattern.MatchString(schemaName) {
		return nil, func() {}, fmt.Errorf("invalid schema %q; use letters, digits, and underscores, starting with a letter or underscore", schemaName)
	}
	adminDB, err := core.OpenPostgres(postgresDSN)
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		adminDB.Close()
	}
	if _, err := adminDB.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("drop old schema: %w", err)
	}
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("create schema: %w", err)
	}
	if !keepSchema {
		previousCleanup := cleanup
		cleanup = func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			if _, err := adminDB.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
				log.Printf("cleanup schema %s: %v", schemaName, err)
			}
			previousCleanup()
		}
	}
	c, err := core.NewPostgres(withSearchPath(postgresDSN, schemaName), options...)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return c, cleanup, nil
}

func defaultCommandLogLoadNATSStream() string {
	return fmt.Sprintf("BUDGIE_COMMAND_LOG_LOAD_%d_%d", os.Getpid(), time.Now().UnixNano())
}

func defaultEventLogLoadNATSStream() string {
	return fmt.Sprintf("BUDGIE_EVENT_LOG_LOAD_%d_%d", os.Getpid(), time.Now().UnixNano())
}

func defaultCommandLogLoadIndexPrefix() string {
	return fmt.Sprintf("budgie:commandlog-load:%d:%d", os.Getpid(), time.Now().UnixNano())
}

func normalizeCommandLogLoadExecutorMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "sql", "postgres", "postgresql":
		return core.CommandLogDrainExecutorSQL
	case "native", "broker-native", "event-transaction":
		return core.CommandLogDrainExecutorNative
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func isSupportedCommandLogLoadExecutorMode(mode string) bool {
	return mode == core.CommandLogDrainExecutorSQL || mode == core.CommandLogDrainExecutorNative
}

type commandLogLoadRuntimeConfig struct {
	PostgresDSN           string
	RequirePostgres       bool
	PostgresSchema        string
	KeepPostgresSchema    bool
	NATSURL               string
	Backend               string
	ExecutorMode          string
	CommandNATSStream     string
	CommandNATSReplicas   int
	EventNATSStream       string
	EventNATSReplicas     int
	CommandLogIndex       string
	CommandLogIndexPrefix string
	RedisURL              string
	ScalarAllocator       string
	Kafka                 kafkaconn.RuntimeConfig
	KafkaPartitions       int32
	KafkaEventPartitions  int32
	KafkaTopicReplicas    int
}

func validateCommandLogLoadRuntimeConfig(config commandLogLoadRuntimeConfig) error {
	if config.RequirePostgres && strings.TrimSpace(config.PostgresDSN) == "" {
		return fmt.Errorf("-require-postgres requires -postgres-dsn or BUDGIE_POSTGRES_DSN")
	}
	backend := normalizeCommandLogLoadBackend(config.Backend)
	switch normalizeCommandLogLoadIndexBackend(config.CommandLogIndex) {
	case "":
	case "redis":
		if strings.TrimSpace(config.RedisURL) == "" {
			return fmt.Errorf("-command-log-index redis requires -redis or BUDGIE_REDIS_URL")
		}
	default:
		return fmt.Errorf("unsupported -command-log-index %q; supported: redis", config.CommandLogIndex)
	}
	if config.ExecutorMode == core.CommandLogDrainExecutorNative && backend == "nats" {
		if strings.TrimSpace(config.PostgresDSN) == "" {
			return fmt.Errorf("native NATS command-log load requires -postgres-dsn or BUDGIE_POSTGRES_DSN; temporary SQLite materialization is not a durable staging gate")
		}
		commandStream := strings.TrimSpace(config.CommandNATSStream)
		eventStream := strings.TrimSpace(config.EventNATSStream)
		if commandStream != "" && eventStream != "" && commandStream == eventStream {
			return fmt.Errorf("native NATS command-log load requires distinct command and event streams, got %q", commandStream)
		}
	}
	if backend == "kafka" {
		if config.KafkaTopicReplicas <= 0 {
			return fmt.Errorf("kafka command-log load requires -kafka-topic-replicas to be positive")
		}
		if config.KafkaTopicReplicas > 32767 {
			return fmt.Errorf("kafka command-log load requires -kafka-topic-replicas to fit int16, got %d", config.KafkaTopicReplicas)
		}
		if config.ExecutorMode == core.CommandLogDrainExecutorNative {
			if err := config.Kafka.ValidateCommandEventTransaction(); err != nil {
				return err
			}
			if config.KafkaPartitions <= 0 {
				return fmt.Errorf("native Kafka command-log load requires -kafka-command-partitions for logical command-partition mapping")
			}
			if config.KafkaEventPartitions <= 0 {
				return fmt.Errorf("native Kafka command-log load requires -kafka-event-partitions for logical event-partition mapping")
			}
			scalarAllocator := commandLogLoadScalarCompatibilityAllocator(backend, config.ExecutorMode, config.ScalarAllocator)
			switch scalarAllocator {
			case core.CommandLogDrainScalarAllocatorSQLEventOffsets, core.CommandLogDrainScalarAllocatorSQLEventPartitions:
			default:
				return fmt.Errorf("native Kafka command-log load has unsupported -kafka-scalar-allocator %q; supported: %s,%s",
					config.ScalarAllocator,
					core.CommandLogDrainScalarAllocatorSQLEventOffsets,
					core.CommandLogDrainScalarAllocatorSQLEventPartitions)
			}
		} else if err := config.Kafka.ValidateCommandLog(); err != nil {
			return err
		} else if config.KafkaPartitions <= 0 {
			return fmt.Errorf("kafka command-log load requires -kafka-command-partitions for logical partition mapping")
		}
	}
	return nil
}

func ensureCommandLogLoadKafkaTopics(ctx context.Context, config commandLogLoadRuntimeConfig) error {
	if normalizeCommandLogLoadBackend(config.Backend) != "kafka" {
		return nil
	}
	specs, err := commandLogLoadKafkaTopicSpecs(config)
	if err != nil {
		return err
	}
	return kafkaconn.EnsureTopics(ctx, kafkaconn.TopicProvisioningOptions{
		Runtime:  config.Kafka,
		ClientID: fmt.Sprintf("budgie-commandlog-loadgen-%d-topic-setup", os.Getpid()),
		Topics:   specs,
	})
}

func commandLogLoadKafkaTopicSpecs(config commandLogLoadRuntimeConfig) ([]kafkaconn.TopicProvisioningSpec, error) {
	runtime := config.Kafka.Normalize()
	replicationFactor := config.KafkaTopicReplicas
	if replicationFactor <= 0 {
		return nil, fmt.Errorf("kafka command-log load requires -kafka-topic-replicas to be positive")
	}
	if replicationFactor > 32767 {
		return nil, fmt.Errorf("kafka command-log load requires -kafka-topic-replicas to fit int16, got %d", replicationFactor)
	}
	specs := []kafkaconn.TopicProvisioningSpec{{
		Topic:             runtime.CommandTopic,
		Partitions:        config.KafkaPartitions,
		ReplicationFactor: int16(replicationFactor),
	}}
	if config.ExecutorMode == core.CommandLogDrainExecutorNative {
		specs = append(specs, kafkaconn.TopicProvisioningSpec{
			Topic:             runtime.EventTopic,
			Partitions:        config.KafkaEventPartitions,
			ReplicationFactor: int16(replicationFactor),
		})
	}
	return specs, nil
}

func commandLogLoadRuntimeReport(config commandLogLoadRuntimeConfig) core.CommandLogDrainLoadRuntime {
	backend := normalizeCommandLogLoadBackend(config.Backend)
	materialization := "sqlite"
	if strings.TrimSpace(config.PostgresDSN) != "" {
		materialization = "postgres"
	}
	eventBackend := ""
	if config.ExecutorMode == core.CommandLogDrainExecutorNative {
		eventBackend = backend
	}
	indexBackend := normalizeCommandLogLoadIndexBackend(config.CommandLogIndex)
	runtime := core.CommandLogDrainLoadRuntime{
		CommandLogBackend:            backend,
		EventLogBackend:              eventBackend,
		MaterializationStore:         materialization,
		ScalarCompatibilityAllocator: commandLogLoadScalarCompatibilityAllocator(backend, config.ExecutorMode, config.ScalarAllocator),
		PostgresEndpoint:             sanitizedCommandLogLoadEndpoint(config.PostgresDSN, "postgres"),
		RequirePostgres:              config.RequirePostgres,
		PostgresSchema:               strings.TrimSpace(config.PostgresSchema),
		KeepPostgresSchema:           config.KeepPostgresSchema,
	}
	if indexBackend != "" {
		runtime.CommandLogIndexBackend = indexBackend
		runtime.CommandLogIndexPrefix = strings.TrimSpace(config.CommandLogIndexPrefix)
		if indexBackend == "redis" {
			runtime.RedisEndpoint = sanitizedCommandLogLoadEndpoint(config.RedisURL, "redis")
		}
	}
	if backend == "nats" {
		runtime.NATSEndpoint = sanitizedCommandLogLoadEndpoint(config.NATSURL, "nats")
		runtime.CommandNATSStream = strings.TrimSpace(config.CommandNATSStream)
		runtime.CommandNATSReplicas = effectiveCommandLogLoadNATSReplicas(config.CommandNATSReplicas)
	}
	if eventBackend == "nats" {
		runtime.EventNATSStream = strings.TrimSpace(config.EventNATSStream)
		runtime.EventNATSReplicas = effectiveCommandLogLoadNATSReplicas(config.EventNATSReplicas)
	}
	if backend == "kafka" {
		kafka := config.Kafka.Normalize()
		runtime.KafkaBrokers = sanitizedKafkaBrokerEndpoints(kafka.Brokers)
		runtime.KafkaTLS = kafka.TLS
		runtime.KafkaSASLMechanism = kafka.SASLMechanism
		runtime.KafkaCommandTopic = kafka.CommandTopic
		runtime.KafkaConsumerGroup = kafka.ConsumerGroup
		runtime.KafkaCommandPartitions = int(config.KafkaPartitions)
	}
	if eventBackend == "kafka" {
		kafka := config.Kafka.Normalize()
		runtime.KafkaEventTopic = kafka.EventTopic
		runtime.KafkaEventPartitions = int(config.KafkaEventPartitions)
	}
	runtime.DurableStaging = isDurableCommandLogLoadBackend(backend) && materialization == "postgres" &&
		(config.ExecutorMode != core.CommandLogDrainExecutorNative || eventBackend == backend)
	return runtime
}

func normalizeCommandLogLoadIndexBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "off", "disabled":
		return ""
	case "redis":
		return "redis"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func commandLogLoadScalarCompatibilityAllocator(backend, executorMode, raw string) string {
	if executorMode != core.CommandLogDrainExecutorNative {
		return core.CommandLogDrainScalarAllocatorPostgresEventSeq
	}
	switch normalizeCommandLogLoadBackend(backend) {
	case "kafka":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "", "sql", "sql-scalar", "sql-event-scalar-offset", core.CommandLogDrainScalarAllocatorSQLEventOffsets:
			return core.CommandLogDrainScalarAllocatorSQLEventOffsets
		case "partition", "partition-only", "sql-partition", "sql-partition-offsets", "sql-event-partition-offset", core.CommandLogDrainScalarAllocatorSQLEventPartitions:
			return core.CommandLogDrainScalarAllocatorSQLEventPartitions
		default:
			return strings.ToLower(strings.TrimSpace(raw))
		}
	case "nats":
		return core.CommandLogDrainScalarAllocatorBrokerStreamSequence
	case "", "memory":
		return core.CommandLogDrainScalarAllocatorMemoryStreamSequence
	default:
		return strings.ToLower(strings.TrimSpace(backend))
	}
}

func isDurableCommandLogLoadBackend(backend string) bool {
	return backend == "nats" || backend == "kafka"
}

func effectiveCommandLogLoadNATSReplicas(replicas int) int {
	if replicas <= 0 {
		return 1
	}
	return replicas
}

func sanitizedCommandLogLoadEndpoint(raw, fallbackScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	fields := commandLogLoadDSNFields(raw)
	host := strings.TrimSpace(fields["host"])
	port := strings.TrimSpace(fields["port"])
	dbname := strings.TrimSpace(fields["dbname"])
	if host == "" && dbname == "" {
		return strings.TrimSpace(fallbackScheme) + "-endpoint"
	}
	if port != "" && !strings.Contains(host, ":") {
		host += ":" + port
	}
	endpoint := strings.TrimSpace(fallbackScheme) + "://"
	if host != "" {
		endpoint += host
	}
	if dbname != "" {
		endpoint += "/" + url.PathEscape(dbname)
	}
	return endpoint
}

func sanitizedKafkaBrokerEndpoints(brokers []string) []string {
	out := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		if !strings.Contains(broker, "://") {
			broker = "kafka://" + broker
		}
		out = append(out, sanitizedCommandLogLoadEndpoint(broker, "kafka"))
	}
	return out
}

func commandLogLoadDSNFields(raw string) map[string]string {
	fields := map[string]string{}
	for _, field := range strings.Fields(raw) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), "'\"")
		fields[key] = value
	}
	return fields
}

func commandLogLoadPostgresSchema(postgresDSN, schema string) string {
	if strings.TrimSpace(postgresDSN) == "" {
		return ""
	}
	schema = strings.TrimSpace(schema)
	if schema != "" {
		return schema
	}
	return fmt.Sprintf("budgie_cmdlog_load_%d_%d", os.Getpid(), time.Now().UnixNano())
}

func commandLogLoadEvidence(budgetFile string) core.CommandLogDrainLoadEvidence {
	budgetFile = strings.TrimSpace(budgetFile)
	evidence := core.CommandLogDrainLoadEvidence{
		Tool:       "budgie-commandlog-loadgen",
		BudgetFile: budgetFile,
	}
	if budgetFile != "" {
		if data, err := os.ReadFile(budgetFile); err == nil {
			sum := sha256.Sum256(data)
			evidence.BudgetSHA256 = fmt.Sprintf("%x", sum)
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				evidence.GitRevision = setting.Value
			case "vcs.modified":
				evidence.GitModified = setting.Value == "true"
			}
		}
	}
	if strings.TrimSpace(evidence.GitRevision) == "" {
		if revision, ok := commandLogLoadGitOutput("rev-parse", "HEAD"); ok {
			evidence.GitRevision = revision
		}
	}
	if status, ok := commandLogLoadGitOutput("status", "--porcelain"); ok && strings.TrimSpace(status) != "" {
		evidence.GitModified = true
	}
	return evidence
}

func attachCommandLogLoadScalarCompatibilityAudit(ctx context.Context, db *sql.DB, report *core.CommandLogDrainLoadReport) error {
	if report == nil || db == nil {
		return nil
	}
	if normalizeCommandLogLoadBackend(report.Runtime.MaterializationStore) != "postgres" {
		return nil
	}
	var offset int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE((
		     SELECT last_seq
		       FROM event_scalar_offsets
		      WHERE id='broker_event_log'
		   ), 0)`,
	).Scan(&offset); err != nil {
		return err
	}
	report.ScalarCompatibilityAudit = core.CommandLogScalarCompatibilityAudit{
		Enabled:                    true,
		Store:                      "event_scalar_offsets",
		OffsetID:                   "broker_event_log",
		LegacySQLScalarOffsetAfter: offset,
	}
	return nil
}

func commandLogLoadGitOutput(args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func normalizeCommandLogLoadBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "memory":
		return "memory"
	case "nats", "jetstream":
		return "nats"
	case "kafka", "redpanda":
		return "kafka"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func printReport(report core.CommandLogDrainLoadReport, pretty bool) error {
	encoder := json.NewEncoder(os.Stdout)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(report)
}

func withSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
