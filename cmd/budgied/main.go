// budgied is the BudgieBBS server — HTTP, WebSocket, and SSH all in one binary.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/mailer"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
	"github.com/juncoflockleader/budgie-bbs/internal/nntp"
	"github.com/juncoflockleader/budgie-bbs/internal/redisconn"
	"github.com/juncoflockleader/budgie-bbs/internal/tui"
	"github.com/juncoflockleader/budgie-bbs/internal/wsapi"
)

func main() {
	kafkaSecurityDefaults := kafkaconn.RuntimeSecurityConfigFromEnv()
	var (
		dbPath                              = flag.String("db", "budgie.db", "SQLite database path")
		storage                             = flag.String("storage", "sqlite", "Storage backend: sqlite or postgres")
		httpAddr                            = flag.String("http", ":8080", "HTTP/WebSocket listen address")
		sshPort                             = flag.Int("ssh", 2222, "SSH listen port")
		hostKey                             = flag.String("hostkey", "", "Path to SSH host key (auto-generated if empty)")
		jwtSecret                           = flag.String("jwt-secret", "", "JWT signing secret (random if empty)")
		captchaMode                         = flag.String("captcha-mode", "", "Signup captcha mode: off, native, or provider (also BUDGIE_CAPTCHA_MODE)")
		captchaProvider                     = flag.String("captcha-provider", "", "Captcha provider for -captcha-mode provider: recaptcha, hcaptcha, or turnstile (also BUDGIE_CAPTCHA_PROVIDER)")
		captchaSiteKey                      = flag.String("captcha-site-key", "", "Public captcha site key (also BUDGIE_CAPTCHA_SITE_KEY)")
		captchaSecret                       = flag.String("captcha-secret", "", "Captcha provider secret / native HMAC key (also BUDGIE_CAPTCHA_SECRET)")
		captchaVerifyURL                    = flag.String("captcha-verify-url", "", "Override captcha provider verify URL (optional)")
		mailMode                            = flag.String("mail-mode", "", "Outbound email: off, direct (to MX), or relay (also BUDGIE_MAIL_MODE; defaults to relay if -smtp-host set, else direct when -mail-from set)")
		mailFromAddr                        = flag.String("mail-from", "", "From address for outbound email; required to send (also BUDGIE_MAIL_FROM)")
		smtpHost                            = flag.String("smtp-host", "", "SMTP relay host (also BUDGIE_SMTP_HOST); how an email provider/ESP is wired in")
		smtpPort                            = flag.Int("smtp-port", 587, "SMTP relay port")
		smtpUser                            = flag.String("smtp-user", "", "SMTP relay username (also BUDGIE_SMTP_USER)")
		smtpPassword                        = flag.String("smtp-password", "", "SMTP relay password (also BUDGIE_SMTP_PASSWORD)")
		requireEmailVerify                  = flag.Bool("require-email-verification", true, "Require email verification before login when a mailer is configured")
		publicURL                           = flag.String("public-url", "", "Public base URL for email verification links (also BUDGIE_PUBLIC_URL)")
		mailInboxURL                        = flag.String("mail-inbox-url", "", "Web inbox URL of a local SMTP catcher to surface in the signup UI; auto-set to http://localhost:8025 (mailpit) when the relay host is loopback (also BUDGIE_MAIL_INBOX_URL)")
		webRoot                             = flag.String("web", "", "Path to web/dist directory for SPA serving (optional)")
		nntpAddr                            = flag.String("nntp", "", "NNTP listen address (optional, e.g. :1190)")
		nntpDomain                          = flag.String("nntp-domain", "budgie.local", "Domain used for NNTP Message-ID values")
		nntpPrefix                          = flag.String("nntp-prefix", "budgie", "NNTP newsgroup prefix")
		migratePG                           = flag.Bool("migrate-sqlite-to-postgres", false, "Migrate SQLite source DB into PostgreSQL and exit")
		pgDSN                               = flag.String("postgres-dsn", "", "PostgreSQL DSN (also read from BUDGIE_POSTGRES_DSN)")
		natsURL                             = flag.String("nats", "", "NATS URL for cross-node event delivery (also read from BUDGIE_NATS_URL)")
		redisURL                            = flag.String("redis", "", "Redis URL for optional Redis-backed runtime features (also read from BUDGIE_REDIS_URL)")
		kafkaBrokers                        = flag.String("kafka-brokers", "", "Comma-separated Kafka/Redpanda brokers for kafka/redpanda log backends (also read from BUDGIE_KAFKA_BROKERS)")
		kafkaCommandTopic                   = flag.String("kafka-command-topic", kafkaconn.DefaultCommandTopic, "Kafka/Redpanda command-log topic")
		kafkaEventTopic                     = flag.String("kafka-event-topic", kafkaconn.DefaultEventTopic, "Kafka/Redpanda event-log topic")
		kafkaConsumerGroup                  = flag.String("kafka-consumer-group", kafkaconn.DefaultWriterConsumerGroup, "Kafka/Redpanda writer consumer group")
		kafkaCommandPartitions              = flag.Int("kafka-command-partitions", 0, "Kafka/Redpanda command-topic partition count for logical-partition mapping")
		kafkaEventPartitions                = flag.Int("kafka-event-partitions", 0, "Kafka/Redpanda event-topic partition count for logical event-partition mapping")
		kafkaTLS                            = flag.Bool("kafka-tls", kafkaSecurityDefaults.TLS, "Enable TLS for Kafka/Redpanda connections (also read from BUDGIE_KAFKA_TLS)")
		kafkaTLSCAFile                      = flag.String("kafka-tls-ca-file", kafkaSecurityDefaults.TLSCAFile, "Optional PEM CA bundle for Kafka/Redpanda TLS (also read from BUDGIE_KAFKA_TLS_CA_FILE)")
		kafkaTLSServerName                  = flag.String("kafka-tls-server-name", kafkaSecurityDefaults.TLSServerName, "Optional TLS server name override for Kafka/Redpanda (also read from BUDGIE_KAFKA_TLS_SERVER_NAME)")
		kafkaSASLMechanism                  = flag.String("kafka-sasl-mechanism", kafkaSecurityDefaults.SASLMechanism, "Kafka/Redpanda SASL mechanism: plain, scram-sha-256, or scram-sha-512 (also read from BUDGIE_KAFKA_SASL_MECHANISM)")
		kafkaSASLUser                       = flag.String("kafka-sasl-user", kafkaSecurityDefaults.SASLUser, "Kafka/Redpanda SASL user (also read from BUDGIE_KAFKA_SASL_USER)")
		kafkaSASLPassword                   = flag.String("kafka-sasl-password", kafkaSecurityDefaults.SASLPassword, "Kafka/Redpanda SASL password (also read from BUDGIE_KAFKA_SASL_PASSWORD)")
		writeRegionURL                      = flag.String("write-region-url", "", "Authoritative write-region HTTP base URL for regional nodes; mutating HTTP API requests are proxied there (also read from BUDGIE_WRITE_REGION_URL)")
		hotThreadSplits                     = flag.String("hot-thread-splits", "", "Comma-separated hot thread reply splits as thread_id=shards; use the same value on gateway and writer nodes")
		commandLogShadow                    = flag.String("command-log-shadow", "", "Shadow command log backend: off, memory, nats, or kafka/redpanda")
		commandLogShadowNATSStream          = flag.String("command-log-shadow-nats-stream", "BUDGIE_COMMAND_LOG", "NATS JetStream stream name for -command-log-shadow nats")
		commandLogShadowNATSReplicas        = flag.Int("command-log-shadow-nats-replicas", 1, "NATS JetStream replica count for -command-log-shadow nats")
		commandLogAuthoritative             = flag.String("command-log-authoritative", "", "Authoritative command-log submit backend: off, memory, nats, or kafka/redpanda")
		commandLogAuthoritativeNATSStream   = flag.String("command-log-authoritative-nats-stream", "BUDGIE_COMMAND_LOG", "NATS JetStream stream name for -command-log-authoritative nats")
		commandLogAuthoritativeNATSReplicas = flag.Int("command-log-authoritative-nats-replicas", 1, "NATS JetStream replica count for -command-log-authoritative nats")
		commandLogWorker                    = flag.String("command-log-worker", "", "Experimental command-log writer backend: off, memory, nats, or kafka/redpanda for SQL executor")
		commandLogWorkerNATSStream          = flag.String("command-log-worker-nats-stream", "BUDGIE_COMMAND_LOG", "NATS JetStream stream name for -command-log-worker nats")
		commandLogWorkerNATSReplicas        = flag.Int("command-log-worker-nats-replicas", 1, "NATS JetStream replica count for -command-log-worker nats")
		commandLogWorkerBatchSize           = flag.Int("command-log-worker-batch-size", 100, "Maximum command-log records fetched per partition drain")
		commandLogWorkerConcurrency         = flag.Int("command-log-worker-partition-concurrency", 1, "Maximum command-log partitions drained concurrently by this writer")
		commandLogWorkerLimit               = flag.Int("command-log-worker-partition-limit", 100, "Maximum command-log partitions checked per drain")
		commandLogWorkerInterval            = flag.Duration("command-log-worker-interval", time.Second, "Interval between command-log writer drains")
		commandLogWorkerClaimTTL            = flag.Duration("command-log-worker-claim-ttl", 30*time.Second, "How long a command-log writer owns a partition lease before another writer may take over")
		commandLogWorkerClaimRefresh        = flag.Duration("command-log-worker-claim-refresh-interval", 0, "Interval for refreshing a command-log partition lease during command execution (default: one third of claim TTL)")
		commandLogWorkerOwnership           = flag.String("command-log-worker-ownership", "sql-lease", "Experimental command-log writer ownership: sql-lease, hash-assignment, or nats-kv")
		commandLogWorkerGroupMembers        = flag.String("command-log-worker-group-members", "", "Comma-separated writer owner ids for hash-assignment or nats-kv ownership")
		commandLogWorkerAssignmentOverrides = flag.String("command-log-worker-assignment-overrides", "", "Comma-separated command partition owner overrides as kind/key=writer for hash-assignment or nats-kv ownership")
		commandLogWorkerAssignBucket        = flag.String("command-log-worker-assignment-bucket", "", "NATS JetStream KV bucket for nats-kv command-log writer ownership")
		commandLogWorkerID                  = flag.String("command-log-worker-id", defaultCommandLogWorkerID(), "Stable owner id used for command-log partition leases")
		commandLogWorkerExecutor            = flag.String("command-log-worker-executor", "sql", "Experimental command-log writer executor: sql or native")
		commandLogWorkerEventNATSStream     = flag.String("command-log-worker-event-nats-stream", "BUDGIE_EVENT_LOG", "NATS JetStream event-log stream name for -command-log-worker-executor native")
		commandLogWorkerEventNATSReplicas   = flag.Int("command-log-worker-event-nats-replicas", 1, "NATS JetStream event-log replica count for -command-log-worker-executor native")
		auditCommandLogMaterialization      = flag.Bool("audit-command-log-materialization", false, "Audit committed command-log offsets against SQL materialization receipts and exit; uses -command-log-worker backend/stream flags")
		checkCommandLogPromotionReadiness   = flag.Bool("check-command-log-promotion-readiness", false, "Check command-log promotion readiness and exit; requires zero uncommitted lag plus complete SQL materialization audit")
		eventLogShadow                      = flag.String("event-log-shadow", "", "Shadow event log backend for parity checks: off, memory, nats, or kafka/redpanda")
		eventLogShadowInterval              = flag.Duration("event-log-shadow-interval", 30*time.Second, "Interval for shadow event-log replay parity checks")
		eventLogShadowReplayLimit           = flag.Int("event-log-shadow-replay-limit", 100, "Maximum events per partition replay parity window")
		eventLogShadowPartitionLimit        = flag.Int("event-log-shadow-partition-limit", 100, "Maximum event partitions checked per parity interval")
		eventLogShadowStartHead             = flag.Bool("event-log-shadow-start-at-head", true, "Seed shadow parity checkpoints at current SQL heads before tail checking")
		eventLogShadowNATSStream            = flag.String("event-log-shadow-nats-stream", "BUDGIE_EVENT_LOG", "NATS JetStream stream name for -event-log-shadow nats")
		eventLogShadowNATSReplicas          = flag.Int("event-log-shadow-nats-replicas", 1, "NATS JetStream replica count for -event-log-shadow nats")
		checkEventLogPromotionReadiness     = flag.Bool("check-event-log-promotion-readiness", false, "Check SQL-vs-shadow event-log promotion readiness and exit; uses -event-log-shadow nats|kafka")
		eventStoreProjection                = flag.String("event-store-projection", "", "Experimental broker event-store projection worker backend: off, nats, or kafka/redpanda")
		eventStoreProjectionNATSStream      = flag.String("event-store-projection-nats-stream", "BUDGIE_EVENT_LOG", "NATS JetStream stream name for -event-store-projection nats")
		eventStoreProjectionNATSReplicas    = flag.Int("event-store-projection-nats-replicas", 1, "NATS JetStream replica count for -event-store-projection nats")
		eventStoreProjectionSource          = flag.String("event-store-projection-source", "", "Source name used in event-store projection watermarks; defaults to the projection backend")
		eventStoreProjectionInterval        = flag.Duration("event-store-projection-interval", 5*time.Second, "Interval between broker event-store projection drains")
		eventStoreProjectionBatchSize       = flag.Int("event-store-projection-batch-size", 100, "Maximum broker events applied per partition projection batch")
		eventStoreProjectionPartitionLimit  = flag.Int("event-store-projection-partition-limit", 100, "Maximum broker event partitions checked per projection drain")
		rebuild                             = flag.Bool("rebuild-projections", false, "Rebuild projection tables from durable events and exit")
		rebuildSource                       = flag.String("rebuild-source", "sql", "Projection rebuild source: sql, nats, or kafka/redpanda")
		rebuildSeq                          = flag.Int64("rebuild-from-seq", 0, "Replay durable events with seq > value during projection rebuild")
		derivedViewBackfill                 = flag.String("backfill-derived-views", "", "Comma-separated derived views, groups (search,rankings,summaries,community,feeds), or all, to rebuild from the event log and mark applied; uses -rebuild-source and -rebuild-from-seq")
		derivedViewWatermarks               = flag.String("derived-view-watermarks", "", "Comma-separated derived views, groups (search,rankings,summaries,community,feeds), or all, whose watermarks this worker periodically advances to durable head")
		derivedViewWatermarkInterval        = flag.Duration("derived-view-watermark-interval", 5*time.Second, "Interval between derived-view watermark syncs")
		derivedViewProcessors               = flag.String("derived-view-processors", "", "Comma-separated dedicated derived-view processor groups (search,rankings,summaries,community,feeds) or all to enable on this worker")
		asyncPostSearch                     = flag.Bool("async-post-search", false, "Do not update search.posts in command transactions; run a post-search processor to catch up from the durable event log")
		postSearchIndexBackend              = flag.String("post-search-index", "sql-fts", "Post search index backend: sql-fts or meilisearch")
		meiliSearchURL                      = flag.String("meilisearch-url", "", "Meilisearch base URL for -post-search-index meilisearch (also read from BUDGIE_MEILISEARCH_URL)")
		meiliSearchAPIKey                   = flag.String("meilisearch-api-key", "", "Meilisearch API key for -post-search-index meilisearch (also read from BUDGIE_MEILISEARCH_API_KEY)")
		meiliSearchIndex                    = flag.String("meilisearch-index", "budgie_posts", "Meilisearch index name for post search")
		meiliSearchTaskTimeout              = flag.Duration("meilisearch-task-timeout", 30*time.Second, "Maximum time to wait for Meilisearch document mutation tasks before advancing search.posts")
		meiliSearchPollInterval             = flag.Duration("meilisearch-task-poll-interval", 200*time.Millisecond, "Interval between Meilisearch task status polls")
		postSearchProcessor                 = flag.Bool("post-search-processor", false, "Run the search.posts event-log processor on this worker node")
		postSearchProcessorInterval         = flag.Duration("post-search-processor-interval", time.Second, "Interval between search.posts processor drains")
		postSearchProcessorBatchSize        = flag.Int("post-search-processor-batch-size", 500, "Maximum durable events the search.posts processor applies per batch")
		digestSearchProcessor               = flag.Bool("digest-search-processor", false, "Run the search.digest event-log processor on this worker node")
		digestSearchProcessorInterval       = flag.Duration("digest-search-processor-interval", time.Second, "Interval between search.digest processor drains")
		digestSearchProcessorBatchSize      = flag.Int("digest-search-processor-batch-size", 500, "Maximum durable events the search.digest processor applies per batch")
		communityStatsProcessor             = flag.Bool("community-stats-processor", false, "Run the community_stats event-log processor on this worker node")
		communityStatsProcessorInterval     = flag.Duration("community-stats-processor-interval", time.Second, "Interval between community_stats processor drains")
		communityStatsProcessorBatchSize    = flag.Int("community-stats-processor-batch-size", 500, "Maximum durable events the community_stats processor applies per batch")
		latestFeedProcessor                 = flag.Bool("latest-feed-processor", false, "Run the feeds.latest event-log processor on this worker node")
		latestFeedProcessorInterval         = flag.Duration("latest-feed-processor-interval", time.Second, "Interval between feeds.latest processor drains")
		latestFeedProcessorBatchSize        = flag.Int("latest-feed-processor-batch-size", 500, "Maximum durable events the feeds.latest processor applies per batch")
		residentFeedProcessor               = flag.Bool("resident-feed-processor", false, "Run the feeds.resident event-log processor on this worker node")
		residentFeedProcessorInterval       = flag.Duration("resident-feed-processor-interval", time.Second, "Interval between feeds.resident processor drains")
		residentFeedProcessorBatchSize      = flag.Int("resident-feed-processor-batch-size", 500, "Maximum durable events the feeds.resident processor applies per batch")
		boardSummariesProcessor             = flag.Bool("board-summaries-processor", false, "Run the summaries.boards event-log processor on this worker node")
		boardSummariesProcessorInterval     = flag.Duration("board-summaries-processor-interval", time.Second, "Interval between summaries.boards processor drains")
		boardSummariesProcessorBatchSize    = flag.Int("board-summaries-processor-batch-size", 500, "Maximum durable events the summaries.boards processor applies per batch")
		unreadThreadSummariesProcessor      = flag.Bool("unread-thread-summaries-processor", false, "Run the summaries.unread_threads event-log processor on this worker node")
		unreadThreadSummariesInterval       = flag.Duration("unread-thread-summaries-processor-interval", time.Second, "Interval between summaries.unread_threads processor drains")
		unreadThreadSummariesBatchSize      = flag.Int("unread-thread-summaries-processor-batch-size", 500, "Maximum durable events the summaries.unread_threads processor applies per batch")
		boardRankingsProcessor              = flag.Bool("board-rankings-processor", false, "Run the rankings.boards event-log processor on this worker node")
		boardRankingsProcessorInterval      = flag.Duration("board-rankings-processor-interval", time.Second, "Interval between rankings.boards processor drains")
		boardRankingsProcessorBatchSize     = flag.Int("board-rankings-processor-batch-size", 500, "Maximum durable events the rankings.boards processor applies per batch")
		threadRankingsProcessor             = flag.Bool("thread-rankings-processor", false, "Run the rankings.threads event-log processor on this worker node")
		threadRankingsProcessorInterval     = flag.Duration("thread-rankings-processor-interval", time.Second, "Interval between rankings.threads processor drains")
		threadRankingsProcessorBatchSize    = flag.Int("thread-rankings-processor-batch-size", 500, "Maximum durable events the rankings.threads processor applies per batch")
		replyRankingsProcessor              = flag.Bool("reply-rankings-processor", false, "Run the rankings.replies event-log processor on this worker node")
		replyRankingsProcessorInterval      = flag.Duration("reply-rankings-processor-interval", time.Second, "Interval between rankings.replies processor drains")
		replyRankingsProcessorBatchSize     = flag.Int("reply-rankings-processor-batch-size", 500, "Maximum durable events the rankings.replies processor applies per batch")
		userRankingsProcessor               = flag.Bool("user-rankings-processor", false, "Run the rankings.users event-log processor on this worker node")
		userRankingsProcessorInterval       = flag.Duration("user-rankings-processor-interval", time.Second, "Interval between rankings.users processor drains")
		userRankingsProcessorBatchSize      = flag.Int("user-rankings-processor-batch-size", 500, "Maximum durable events the rankings.users processor applies per batch")
		blessingRankingsProcessor           = flag.Bool("blessing-rankings-processor", false, "Run the rankings.blessings event-log processor on this worker node")
		blessingRankingsProcessorInterval   = flag.Duration("blessing-rankings-processor-interval", time.Second, "Interval between rankings.blessings processor drains")
		blessingRankingsProcessorBatchSize  = flag.Int("blessing-rankings-processor-batch-size", 500, "Maximum durable events the rankings.blessings processor applies per batch")
		archiveRankingsProcessor            = flag.Bool("archive-rankings-processor", false, "Run the rankings.archives event-log processor on this worker node")
		archiveRankingsProcessorInterval    = flag.Duration("archive-rankings-processor-interval", time.Second, "Interval between rankings.archives processor drains")
		archiveRankingsProcessorBatchSize   = flag.Int("archive-rankings-processor-batch-size", 500, "Maximum durable events the rankings.archives processor applies per batch")
		asyncCommunityStatHistory           = flag.Bool("async-community-stat-history", false, "Do not refresh community_stat_history inline; enqueue coalesced snapshot jobs for the worker outbox")
		autoStats                           = flag.Bool("auto-stats", true, "Automatically publish the daily BBSLists stats snapshot")
		counterCheckpointInterval           = flag.Duration("counter-checkpoint-interval", 0, "Interval between durable unordered counter checkpoints on worker leaders; 0 disables")
		counterStoreBackend                 = flag.String("counter-store", "sql", "Unordered counter store backend: sql or nats-kv")
		counterStoreNATSBucket              = flag.String("counter-store-nats-bucket", "", "NATS JetStream KV bucket for -counter-store nats-kv")
		counterStoreNATSReplicas            = flag.Int("counter-store-nats-replicas", 1, "NATS JetStream replica count for -counter-store nats-kv")
		counterStoreShards                  = flag.Int("counter-store-shards", 64, "Shard count for non-SQL unordered counter stores")
		repairCounterStoreAggregates        = flag.Bool("repair-counter-store-aggregates", false, "Rebuild NATS KV counter aggregate shards from identity keys and exit")
		presenceStoreBackend                = flag.String("presence-store", "sql", "Presence store backend: sql or nats-kv")
		presenceStoreNATSBucket             = flag.String("presence-store-nats-bucket", "", "NATS JetStream KV bucket for -presence-store nats-kv")
		presenceStoreNATSReplicas           = flag.Int("presence-store-nats-replicas", 1, "NATS JetStream replica count for -presence-store nats-kv")
		presenceStoreNATSTTL                = flag.Duration("presence-store-nats-ttl", 15*time.Minute, "NATS JetStream KV TTL for -presence-store nats-kv")
		chatStoreBackend                    = flag.String("chat-store", "sql", "Chat history store backend: sql or nats-kv")
		chatStoreNATSBucket                 = flag.String("chat-store-nats-bucket", "", "NATS JetStream KV bucket for -chat-store nats-kv")
		chatStoreNATSReplicas               = flag.Int("chat-store-nats-replicas", 1, "NATS JetStream replica count for -chat-store nats-kv")
		readCacheBackend                    = flag.String("read-cache", "", "Read cache backend for hot stable-watermark views: off, memory, or redis (also read from BUDGIE_READ_CACHE)")
		readCachePrefix                     = flag.String("read-cache-prefix", "", "Key prefix for -read-cache redis (also read from BUDGIE_READ_CACHE_PREFIX; default budgie)")
		readCacheTTL                        = flag.Duration("read-cache-ttl", time.Minute, "TTL for Redis read-cache entries")
		doorsConf                           = flag.String("doors", "", "Path to doors.json config file for door games (optional)")
		roleList                            = flag.String("roles", "api,ssh,worker,nntp", "Comma-separated node roles: api,gateway,ssh,worker,nntp,writer")
		initDB                              = flag.Bool("init-db", false, "Apply the database schema/migrations and exit (use before starting a cluster)")
	)
	flag.Parse()

	roles := parseRoles(*roleList)
	commandWorkerMode := normalizeLogBackend(*commandLogWorker)
	commandAuthoritativeMode := normalizeLogBackend(*commandLogAuthoritative)
	commandLogWorkerExecutorMode := normalizeCommandLogWorkerExecutor(*commandLogWorkerExecutor)
	eventStoreProjectionMode := normalizeEventStoreProjectionMode(*eventStoreProjection)
	eventStoreProjectionSourceName := strings.TrimSpace(*eventStoreProjectionSource)
	if eventStoreProjectionSourceName == "" {
		eventStoreProjectionSourceName = eventStoreProjectionMode
	}
	commandLogOneShot := *auditCommandLogMaterialization || *checkCommandLogPromotionReadiness
	commandLogWorkerOwnershipMode := normalizeCommandLogWorkerOwnership(*commandLogWorkerOwnership)
	counterStoreMode := normalizeCounterStoreBackend(*counterStoreBackend)
	presenceStoreMode := normalizePresenceStoreBackend(*presenceStoreBackend)
	chatStoreMode := normalizeChatStoreBackend(*chatStoreBackend)
	readCacheMode := normalizeReadCacheBackend(*readCacheBackend)
	postSearchIndexMode := normalizePostSearchIndexBackend(*postSearchIndexBackend)
	commandLogWorkerGroupMemberIDs := parseCommandLogWorkerGroupMembers(*commandLogWorkerGroupMembers)
	commandLogWorkerOverrides, parseOverridesErr := parseCommandLogWorkerAssignmentOverrides(*commandLogWorkerAssignmentOverrides)
	if parseOverridesErr != nil {
		slog.Error("invalid command log worker assignment overrides", "flag", "-command-log-worker-assignment-overrides", "value", *commandLogWorkerAssignmentOverrides, "err", parseOverridesErr)
		os.Exit(1)
	}
	commandLogWorkerClaimRefreshInterval := effectiveCommandLogWorkerClaimRefreshInterval(
		*commandLogWorkerClaimTTL,
		*commandLogWorkerClaimRefresh,
	)
	var derivedViewWatermarkViews []string
	if strings.TrimSpace(*derivedViewWatermarks) != "" {
		var err error
		derivedViewWatermarkViews, err = core.ResolveDerivedViews([]string{*derivedViewWatermarks})
		if err != nil {
			slog.Error("invalid derived view watermark selection", "views", *derivedViewWatermarks, "err", err)
			os.Exit(1)
		}
	}
	var derivedViewProcessorViews []string
	if strings.TrimSpace(*derivedViewProcessors) != "" {
		var err error
		derivedViewProcessorViews, err = applyDerivedViewProcessorSelection(
			*derivedViewProcessors,
			derivedViewProcessorFlags{
				asyncPostSearch:                asyncPostSearch,
				postSearchProcessor:            postSearchProcessor,
				digestSearchProcessor:          digestSearchProcessor,
				communityStatsProcessor:        communityStatsProcessor,
				asyncCommunityStatHistory:      asyncCommunityStatHistory,
				latestFeedProcessor:            latestFeedProcessor,
				residentFeedProcessor:          residentFeedProcessor,
				boardSummariesProcessor:        boardSummariesProcessor,
				unreadThreadSummariesProcessor: unreadThreadSummariesProcessor,
				boardRankingsProcessor:         boardRankingsProcessor,
				threadRankingsProcessor:        threadRankingsProcessor,
				replyRankingsProcessor:         replyRankingsProcessor,
				userRankingsProcessor:          userRankingsProcessor,
				blessingRankingsProcessor:      blessingRankingsProcessor,
				archiveRankingsProcessor:       archiveRankingsProcessor,
			},
		)
		if err != nil {
			slog.Error("invalid derived view processor selection", "views", *derivedViewProcessors, "err", err)
			os.Exit(1)
		}
	}

	// Resolve storage backend and DSN (reads env var, handles backwards compat).
	*storage, *pgDSN = resolveStorage(*storage, *pgDSN)
	if *natsURL == "" {
		*natsURL = envOr("BUDGIE_NATS_URL", "")
	}
	if *redisURL == "" {
		*redisURL = envOr("BUDGIE_REDIS_URL", "")
	}
	if *kafkaBrokers == "" {
		*kafkaBrokers = envOr("BUDGIE_KAFKA_BROKERS", "")
	}
	if strings.TrimSpace(*readCacheBackend) == "" {
		*readCacheBackend = envOr("BUDGIE_READ_CACHE", "")
		readCacheMode = normalizeReadCacheBackend(*readCacheBackend)
	}
	if strings.TrimSpace(*readCachePrefix) == "" {
		*readCachePrefix = envOr("BUDGIE_READ_CACHE_PREFIX", "budgie")
	}
	kafkaConfig := kafkaconn.RuntimeConfigFromOptions(*kafkaBrokers, *kafkaCommandTopic, *kafkaEventTopic, *kafkaConsumerGroup, kafkaconn.RuntimeSecurityConfig{
		TLS:           *kafkaTLS,
		TLSCAFile:     *kafkaTLSCAFile,
		TLSServerName: *kafkaTLSServerName,
		SASLMechanism: *kafkaSASLMechanism,
		SASLUser:      *kafkaSASLUser,
		SASLPassword:  *kafkaSASLPassword,
	})
	kafkaCommandPartitionCount := int32(*kafkaCommandPartitions)
	kafkaEventPartitionCount := int32(*kafkaEventPartitions)
	if *writeRegionURL == "" {
		*writeRegionURL = envOr("BUDGIE_WRITE_REGION_URL", "")
	}
	if *meiliSearchURL == "" {
		*meiliSearchURL = envOr("BUDGIE_MEILISEARCH_URL", "")
	}
	if *meiliSearchAPIKey == "" {
		*meiliSearchAPIKey = envOr("BUDGIE_MEILISEARCH_API_KEY", "")
	}

	// JWT secret: use env var, flag, or a fixed dev default.
	secret := []byte(*jwtSecret)
	if len(secret) == 0 {
		secret = []byte(envOr("BUDGIE_JWT_SECRET", "change-me-in-production"))
	}

	if *initDB {
		if *storage != "postgres" || *pgDSN == "" {
			slog.Error("init-db requires postgres storage and a DSN", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
			os.Exit(1)
		}
		// NewPostgres applies the schema/migrations; opening then closing is
		// enough to initialize a fresh database.
		c, err := core.NewPostgres(*pgDSN)
		if err != nil {
			slog.Error("init-db failed", "err", err)
			os.Exit(1)
		}
		_ = c.DB.Close()
		slog.Info("database initialized", "dsn", obfuscateDSN(*pgDSN))
		return
	}

	if *migratePG {
		if *pgDSN == "" {
			slog.Error("postgres DSN required for migration", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := core.MigrateSQLiteToPostgres(ctx, *dbPath, *pgDSN); err != nil {
			slog.Error("sqlite->postgres migration failed", "err", err)
			os.Exit(1)
		}
		slog.Info("sqlite->postgres migration completed", "source", *dbPath, "dsn", obfuscateDSN(*pgDSN))
		return
	}

	if *checkEventLogPromotionReadiness {
		var (
			c   *core.Core
			err error
		)
		if *storage == "postgres" {
			if *pgDSN == "" {
				slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
				os.Exit(1)
			}
			c, err = core.NewPostgres(*pgDSN)
		} else {
			c, err = core.New(*dbPath)
		}
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		defer c.DB.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		readinessBackend := normalizeEventLogPromotionReadinessBackend(*eventLogShadow)
		store, cleanup, err := openEventLogPromotionReadinessStore(
			ctx,
			readinessBackend,
			*natsURL,
			*eventLogShadowNATSStream,
			*eventLogShadowNATSReplicas,
			kafkaConfig,
			kafkaEventPartitionCount,
			fmt.Sprintf("budgie-event-promotion-%d", os.Getpid()),
			c.DB,
		)
		if err != nil {
			slog.Error("event-log promotion source init failed", "backend", readinessBackend, "err", err)
			os.Exit(1)
		}
		defer cleanup()
		report, err := c.CheckEventLogPromotionReadiness(ctx, store, *eventLogShadowReplayLimit, *eventLogShadowPartitionLimit)
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if printErr := encoder.Encode(report); printErr != nil {
			slog.Error("event-log promotion readiness report failed", "err", printErr)
			os.Exit(1)
		}
		if err != nil {
			slog.Error("event-log promotion readiness check failed", "err", err)
			os.Exit(1)
		}
		if !report.Ready {
			attrs := []any{
				"issues", len(report.Issues),
				"partitionsChecked", report.PartitionsChecked,
				"windowsChecked", report.WindowsChecked,
				"compared", report.Compared,
			}
			if len(report.Issues) > 0 {
				first := report.Issues[0]
				attrs = append(attrs,
					"firstIssueKind", first.Kind,
					"firstIssuePartitionKind", first.Partition.Kind,
					"firstIssuePartitionKey", first.Partition.Key,
					"firstIssueMessage", first.Message,
					"firstIssueErr", first.Err,
				)
			}
			slog.Error("event-log promotion readiness check failed", attrs...)
			os.Exit(1)
		}
		slog.Info("event-log promotion readiness check passed",
			"partitionsChecked", report.PartitionsChecked,
			"windowsChecked", report.WindowsChecked,
			"compared", report.Compared)
		return
	}

	if strings.TrimSpace(*derivedViewBackfill) != "" {
		views, err := core.ResolveDerivedViews([]string{*derivedViewBackfill})
		if err != nil {
			slog.Error("invalid derived view backfill selection", "views", *derivedViewBackfill, "err", err)
			os.Exit(1)
		}
		backfillOptions, err := postSearchIndexOptions(
			postSearchIndexMode,
			*meiliSearchURL,
			*meiliSearchAPIKey,
			*meiliSearchIndex,
			*meiliSearchTaskTimeout,
			*meiliSearchPollInterval,
		)
		if err != nil {
			slog.Error("post search index init failed", "backend", postSearchIndexMode, "err", err)
			os.Exit(1)
		}
		var c *core.Core
		if *storage == "postgres" {
			if *pgDSN == "" {
				slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
				os.Exit(1)
			}
			c, err = core.NewPostgres(*pgDSN, backfillOptions...)
		} else {
			c, err = core.New(*dbPath, backfillOptions...)
		}
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		defer c.DB.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		source := normalizeProjectionRebuildSource(*rebuildSource)
		var (
			result      core.DerivedViewBackfillResult
			backfillErr error
		)
		switch source {
		case "sql":
			result, backfillErr = c.BackfillDerivedViewsFromEventLog(views, *rebuildSeq)
		case "nats", "kafka":
			source, store, cleanup, err := openProjectionRebuildEventStore(ctx, projectionRebuildEventStoreOptions{
				Source:               source,
				NATSURL:              *natsURL,
				NATSStream:           *eventLogShadowNATSStream,
				NATSReplicas:         *eventLogShadowNATSReplicas,
				KafkaConfig:          kafkaConfig,
				KafkaEventPartitions: kafkaEventPartitionCount,
				ClientID:             fmt.Sprintf("budgie-derived-backfill-%d", os.Getpid()),
				DB:                   c.DB,
			})
			if err != nil {
				slog.Error("derived-view backfill source init failed", "source", *rebuildSource, "err", err)
				os.Exit(1)
			}
			defer cleanup()
			requireEventLogPromotionReadiness(ctx, c, store, *eventLogShadowReplayLimit, "derived-view backfill", source)
			result, backfillErr = c.BackfillDerivedViewsFromEventStore(ctx, store, views, *rebuildSeq)
		default:
			slog.Error("unsupported derived-view backfill source", "source", *rebuildSource, "supported", "sql,nats,kafka")
			os.Exit(1)
		}
		if backfillErr != nil {
			slog.Error("derived-view backfill failed", "err", backfillErr)
			os.Exit(1)
		}
		if *storage == "postgres" {
			slog.Info("derived-view backfill completed", "storage", "postgres", "source", source, "views", strings.Join(result.Views, ","), "headSeq", result.HeadSeq, "dsn", obfuscateDSN(*pgDSN), "fromSeq", *rebuildSeq)
		} else {
			slog.Info("derived-view backfill completed", "storage", "sqlite", "source", source, "views", strings.Join(result.Views, ","), "headSeq", result.HeadSeq, "db", *dbPath, "fromSeq", *rebuildSeq)
		}
		return
	}

	if *rebuild {
		var (
			c   *core.Core
			err error
		)
		if *storage == "postgres" {
			if *pgDSN == "" {
				slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
				os.Exit(1)
			}
			c, err = core.NewPostgres(*pgDSN)
		} else {
			c, err = core.New(*dbPath)
		}
		if err != nil {
			slog.Error("core init failed", "err", err)
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		source := normalizeProjectionRebuildSource(*rebuildSource)
		var rebuildErr error
		switch source {
		case "sql":
			rebuildErr = c.RebuildProjectionsFromEventLog(*rebuildSeq)
		case "nats", "kafka":
			source, store, cleanup, err := openProjectionRebuildEventStore(ctx, projectionRebuildEventStoreOptions{
				Source:               source,
				NATSURL:              *natsURL,
				NATSStream:           *eventLogShadowNATSStream,
				NATSReplicas:         *eventLogShadowNATSReplicas,
				KafkaConfig:          kafkaConfig,
				KafkaEventPartitions: kafkaEventPartitionCount,
				ClientID:             fmt.Sprintf("budgie-projection-rebuild-%d", os.Getpid()),
				DB:                   c.DB,
			})
			if err != nil {
				slog.Error("projection rebuild source init failed", "source", *rebuildSource, "err", err)
				os.Exit(1)
			}
			defer cleanup()
			requireEventLogPromotionReadiness(ctx, c, store, *eventLogShadowReplayLimit, "projection rebuild", source)
			rebuildErr = c.RebuildProjectionsFromEventStore(ctx, store, *rebuildSeq)
		default:
			slog.Error("unsupported projection rebuild source", "source", *rebuildSource, "supported", "sql,nats,kafka")
			os.Exit(1)
		}
		if rebuildErr != nil {
			slog.Error("projection rebuild failed", "err", rebuildErr)
			os.Exit(1)
		}
		if *storage == "postgres" {
			slog.Info("projection rebuild completed", "storage", "postgres", "source", source, "dsn", obfuscateDSN(*pgDSN), "fromSeq", *rebuildSeq)
		} else {
			slog.Info("projection rebuild completed", "storage", "sqlite", "source", source, "db", *dbPath, "fromSeq", *rebuildSeq)
		}
		return
	}

	// Open core (single path for both storage backends).
	var (
		c                 *core.Core
		err               error
		natsBroker        *natsconn.Conn
		coreOptions       []core.Option
		commandMode       = normalizeLogBackend(*commandLogShadow)
		shadowMode        = normalizeLogBackend(*eventLogShadow)
		commandLog        core.CommandLog
		commandSubmitLog  core.CommandLog
		commandLogMetrics core.CommandLog
		eventProjection   core.EventStore
		commandLogIndex   *core.SQLCommandLogPartitionIndex
		kafkaNativeTx     kafkaNativeTransactionBinder
	)
	hotSplits, err := parseHotThreadSplits(*hotThreadSplits)
	if err != nil {
		slog.Error("invalid hot thread split configuration", "flag", "-hot-thread-splits", "value", *hotThreadSplits, "err", err)
		os.Exit(1)
	}
	if len(hotSplits) > 0 {
		coreOptions = append(coreOptions, core.WithHotThreadSplits(hotSplits))
	}
	if commandMode == "kafka" {
		if err := validateKafkaCommandLogBackend(commandMode, kafkaConfig, kafkaCommandPartitionCount); err != nil {
			slog.Error("invalid command log shadow Kafka config", "backend", *commandLogShadow, "err", err)
			os.Exit(1)
		}
	}
	if !isSupportedCommandLogBackend(commandMode) {
		slog.Error("unsupported command log shadow backend", "backend", *commandLogShadow, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if commandAuthoritativeMode == "kafka" {
		if err := validateKafkaCommandLogBackend(commandAuthoritativeMode, kafkaConfig, kafkaCommandPartitionCount); err != nil {
			slog.Error("invalid authoritative command log Kafka config", "backend", *commandLogAuthoritative, "err", err)
			os.Exit(1)
		}
	}
	if !isSupportedCommandLogBackend(commandAuthoritativeMode) {
		slog.Error("unsupported authoritative command log backend", "backend", *commandLogAuthoritative, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if commandWorkerMode == "kafka" {
		if err := validateKafkaCommandWorkerBackend(commandWorkerMode, commandLogWorkerExecutorMode, kafkaConfig, kafkaCommandPartitionCount, kafkaEventPartitionCount); err != nil {
			slog.Error("invalid command log worker Kafka config", "backend", *commandLogWorker, "executor", commandLogWorkerExecutorMode, "err", err)
			os.Exit(1)
		}
	}
	if !isSupportedCommandLogBackend(commandWorkerMode) {
		slog.Error("unsupported command log worker backend", "backend", *commandLogWorker, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if !isSupportedCommandLogWorkerOwnership(commandLogWorkerOwnershipMode) {
		slog.Error("unsupported command log worker ownership", "ownership", *commandLogWorkerOwnership, "supported", "sql-lease,hash-assignment,nats-kv")
		os.Exit(1)
	}
	if !isSupportedCommandLogWorkerExecutor(commandLogWorkerExecutorMode) {
		slog.Error("unsupported command log worker executor", "executor", *commandLogWorkerExecutor, "supported", "sql,native")
		os.Exit(1)
	}
	if shadowMode == "kafka" {
		if err := validateKafkaEventShadowBackend(shadowMode, kafkaConfig, kafkaEventPartitionCount); err != nil {
			slog.Error("invalid event log shadow Kafka config", "backend", *eventLogShadow, "err", err)
			os.Exit(1)
		}
	}
	if !isSupportedEventLogShadowBackend(shadowMode) {
		slog.Error("unsupported event log shadow backend", "backend", *eventLogShadow, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if !isSupportedEventStoreProjectionMode(eventStoreProjectionMode) {
		slog.Error("unsupported event-store projection backend", "backend", *eventStoreProjection, "supported", "off,nats,kafka")
		os.Exit(1)
	}
	if !isSupportedCounterStoreBackend(counterStoreMode) {
		slog.Error("unsupported counter store backend", "backend", *counterStoreBackend, "supported", "sql,nats-kv")
		os.Exit(1)
	}
	if !isSupportedPresenceStoreBackend(presenceStoreMode) {
		slog.Error("unsupported presence store backend", "backend", *presenceStoreBackend, "supported", "sql,nats-kv")
		os.Exit(1)
	}
	if !isSupportedChatStoreBackend(chatStoreMode) {
		slog.Error("unsupported chat store backend", "backend", *chatStoreBackend, "supported", "sql,nats-kv")
		os.Exit(1)
	}
	if !isSupportedReadCacheBackend(readCacheMode) {
		slog.Error("unsupported read cache backend", "backend", *readCacheBackend, "supported", "off,memory,redis")
		os.Exit(1)
	}
	if readCacheMode == "redis" && *redisURL == "" {
		slog.Error("Redis read cache requires a Redis URL", "flag", "-redis", "env", "BUDGIE_REDIS_URL")
		os.Exit(1)
	}
	if readCacheMode == "redis" && *readCacheTTL <= 0 {
		slog.Error("Redis read cache TTL must be positive", "ttl", readCacheTTL.String())
		os.Exit(1)
	}
	if counterStoreMode == "nats-kv" && *storage != "postgres" {
		slog.Error("NATS KV counter store requires postgres storage", "counterStore", counterStoreMode)
		os.Exit(1)
	}
	if counterStoreMode == "nats-kv" && *natsURL == "" {
		slog.Error("NATS KV counter store requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
		os.Exit(1)
	}
	if counterStoreMode == "nats-kv" && *counterStoreShards <= 0 {
		slog.Error("counter store shard count must be positive", "shards", *counterStoreShards)
		os.Exit(1)
	}
	if *repairCounterStoreAggregates && counterStoreMode != "nats-kv" {
		slog.Error("counter aggregate repair requires the NATS KV counter store", "flag", "-repair-counter-store-aggregates", "counterStore", counterStoreMode)
		os.Exit(1)
	}
	if presenceStoreMode == "nats-kv" && *storage != "postgres" {
		slog.Error("NATS KV presence store requires postgres storage", "presenceStore", presenceStoreMode)
		os.Exit(1)
	}
	if presenceStoreMode == "nats-kv" && *natsURL == "" {
		slog.Error("NATS KV presence store requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
		os.Exit(1)
	}
	if presenceStoreMode == "nats-kv" && *presenceStoreNATSTTL <= 0 {
		slog.Error("presence store NATS TTL must be positive", "ttl", presenceStoreNATSTTL.String())
		os.Exit(1)
	}
	if chatStoreMode == "nats-kv" && *storage != "postgres" {
		slog.Error("NATS KV chat store requires postgres storage", "chatStore", chatStoreMode)
		os.Exit(1)
	}
	if chatStoreMode == "nats-kv" && *natsURL == "" {
		slog.Error("NATS KV chat store requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
		os.Exit(1)
	}
	if !isSupportedPostSearchIndexBackend(postSearchIndexMode) {
		slog.Error("unsupported post search index backend", "backend", *postSearchIndexBackend, "supported", "sql-fts,meilisearch")
		os.Exit(1)
	}
	if postSearchIndexMode == "meilisearch" && strings.TrimSpace(*meiliSearchURL) == "" {
		slog.Error("Meilisearch post search index requires a URL", "flag", "-meilisearch-url", "env", "BUDGIE_MEILISEARCH_URL")
		os.Exit(1)
	}
	if len(derivedViewWatermarkViews) > 0 && !roles["worker"] {
		slog.Error("derived view watermark worker requires the worker role", "views", *derivedViewWatermarks, "roles", *roleList)
		os.Exit(1)
	}
	if len(derivedViewProcessorViews) > 0 && !roles["worker"] {
		slog.Error("derived view processors require the worker role", "views", *derivedViewProcessors, "roles", *roleList)
		os.Exit(1)
	}
	if eventStoreProjectionMode != "" && !roles["worker"] {
		slog.Error("event-store projection worker requires the worker role", "backend", eventStoreProjectionMode, "roles", *roleList)
		os.Exit(1)
	}
	if eventStoreProjectionMode != "" && *storage != "postgres" {
		slog.Error("event-store projection requires postgres storage", "backend", eventStoreProjectionMode)
		os.Exit(1)
	}
	if eventStoreProjectionMode == "nats" && *natsURL == "" {
		slog.Error("NATS event-store projection requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
		os.Exit(1)
	}
	if eventStoreProjectionMode == "kafka" {
		if err := validateKafkaEventProjectionBackend(eventStoreProjectionMode, kafkaConfig, kafkaEventPartitionCount); err != nil {
			slog.Error("invalid Kafka event-store projection config", "backend", *eventStoreProjection, "err", err)
			os.Exit(1)
		}
	}
	if *counterCheckpointInterval > 0 && !roles["worker"] {
		slog.Error("counter checkpoint scheduler requires the worker role", "interval", counterCheckpointInterval.String(), "roles", *roleList)
		os.Exit(1)
	}
	if *postSearchProcessor && !roles["worker"] {
		slog.Error("post search processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *digestSearchProcessor && !roles["worker"] {
		slog.Error("digest search processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *communityStatsProcessor && !roles["worker"] {
		slog.Error("community stats processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *latestFeedProcessor && !roles["worker"] {
		slog.Error("latest feed processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *residentFeedProcessor && !roles["worker"] {
		slog.Error("resident feed processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *boardSummariesProcessor && !roles["worker"] {
		slog.Error("board summaries processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *unreadThreadSummariesProcessor && !roles["worker"] {
		slog.Error("unread thread summaries processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *boardRankingsProcessor && !roles["worker"] {
		slog.Error("board rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *threadRankingsProcessor && !roles["worker"] {
		slog.Error("thread rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *replyRankingsProcessor && !roles["worker"] {
		slog.Error("reply rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *userRankingsProcessor && !roles["worker"] {
		slog.Error("user rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *blessingRankingsProcessor && !roles["worker"] {
		slog.Error("blessing rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if *archiveRankingsProcessor && !roles["worker"] {
		slog.Error("archive rankings processor requires the worker role", "roles", *roleList)
		os.Exit(1)
	}
	if (*asyncPostSearch || *postSearchProcessor || postSearchIndexMode != "sql-fts") && containsString(derivedViewWatermarkViews, core.DerivedViewPostSearch) {
		slog.Error("post search processor ownership cannot use compatibility watermark sync for search.posts",
			"views", *derivedViewWatermarks,
			"hint", "remove search.posts from -derived-view-watermarks and run -post-search-processor on a worker node")
		os.Exit(1)
	}
	if *digestSearchProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewDigestSearch) {
		slog.Error("digest search processor ownership cannot use compatibility watermark sync for search.digest",
			"views", *derivedViewWatermarks,
			"hint", "remove search.digest from -derived-view-watermarks and run -digest-search-processor on a worker node")
		os.Exit(1)
	}
	if *communityStatsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewCommunityStats) {
		slog.Error("community stats processor ownership cannot use compatibility watermark sync for community_stats",
			"views", *derivedViewWatermarks,
			"hint", "remove community_stats from -derived-view-watermarks and run -community-stats-processor on a worker node")
		os.Exit(1)
	}
	if *latestFeedProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewLatestFeed) {
		slog.Error("latest feed processor ownership cannot use compatibility watermark sync for feeds.latest",
			"views", *derivedViewWatermarks,
			"hint", "remove feeds.latest from -derived-view-watermarks and run -latest-feed-processor on a worker node")
		os.Exit(1)
	}
	if *residentFeedProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewResidentFeed) {
		slog.Error("resident feed processor ownership cannot use compatibility watermark sync for feeds.resident",
			"views", *derivedViewWatermarks,
			"hint", "remove feeds.resident from -derived-view-watermarks and run -resident-feed-processor on a worker node")
		os.Exit(1)
	}
	if *boardSummariesProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewBoardSummaries) {
		slog.Error("board summaries processor ownership cannot use compatibility watermark sync for summaries.boards",
			"views", *derivedViewWatermarks,
			"hint", "remove summaries.boards from -derived-view-watermarks and run -board-summaries-processor on a worker node")
		os.Exit(1)
	}
	if *unreadThreadSummariesProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewUnreadThreads) {
		slog.Error("unread thread summaries processor ownership cannot use compatibility watermark sync for summaries.unread_threads",
			"views", *derivedViewWatermarks,
			"hint", "remove summaries.unread_threads from -derived-view-watermarks and run -unread-thread-summaries-processor on a worker node")
		os.Exit(1)
	}
	if *boardRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewBoardRankings) {
		slog.Error("board rankings processor ownership cannot use compatibility watermark sync for rankings.boards",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.boards from -derived-view-watermarks and run -board-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *threadRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewThreadRankings) {
		slog.Error("thread rankings processor ownership cannot use compatibility watermark sync for rankings.threads",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.threads from -derived-view-watermarks and run -thread-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *replyRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewReplyRankings) {
		slog.Error("reply rankings processor ownership cannot use compatibility watermark sync for rankings.replies",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.replies from -derived-view-watermarks and run -reply-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *userRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewUserRankings) {
		slog.Error("user rankings processor ownership cannot use compatibility watermark sync for rankings.users",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.users from -derived-view-watermarks and run -user-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *blessingRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewBlessingRankings) {
		slog.Error("blessing rankings processor ownership cannot use compatibility watermark sync for rankings.blessings",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.blessings from -derived-view-watermarks and run -blessing-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *archiveRankingsProcessor && containsString(derivedViewWatermarkViews, core.DerivedViewArchiveRankings) {
		slog.Error("archive rankings processor ownership cannot use compatibility watermark sync for rankings.archives",
			"views", *derivedViewWatermarks,
			"hint", "remove rankings.archives from -derived-view-watermarks and run -archive-rankings-processor on a worker node")
		os.Exit(1)
	}
	if *asyncCommunityStatHistory && containsString(derivedViewWatermarkViews, core.DerivedViewCommunityStatHistory) {
		slog.Error("community stat history ownership cannot use compatibility watermark sync for community_stat_history",
			"views", *derivedViewWatermarks,
			"hint", "remove community_stat_history from -derived-view-watermarks; queued snapshot jobs advance the view watermark")
		os.Exit(1)
	}
	if commandLogOneShot && commandWorkerMode == "" {
		slog.Error("command-log one-shot check requires a command-log backend", "flag", "-command-log-worker", "supported", supportedCommandLogWorkerBackends())
		os.Exit(1)
	}
	if roles["writer"] && commandWorkerMode == "" {
		slog.Error("writer role requires a command-log worker backend", "flag", "-command-log-worker", "supported", supportedCommandLogWorkerBackends())
		os.Exit(1)
	}
	if commandLogWorkerExecutorMode == "native" && commandWorkerMode == "" {
		slog.Error("native command-log worker executor requires a command-log worker backend", "flag", "-command-log-worker", "supported", supportedNativeCommandLogWorkerBackends())
		os.Exit(1)
	}
	if commandLogWorkerExecutorMode == "native" && commandWorkerMode != "nats" && commandWorkerMode != "kafka" {
		slog.Error("native command-log worker executor requires the NATS or Kafka command-log worker backend",
			"executor", commandLogWorkerExecutorMode,
			"commandLogWorker", commandWorkerMode,
			"hint", "run with -command-log-worker nats or -command-log-worker kafka")
		os.Exit(1)
	}
	if err := validateNativeCommandEventStreams(commandWorkerMode, commandLogWorkerExecutorMode, *commandLogWorkerNATSStream, *commandLogWorkerEventNATSStream); err != nil {
		slog.Error("invalid native command-log stream configuration", "err", err)
		os.Exit(1)
	}
	if err := validateSameProcessNativeWriterProjectionStreams(roles, commandWorkerMode, commandLogWorkerExecutorMode, eventStoreProjectionMode, *commandLogWorkerEventNATSStream, *eventStoreProjectionNATSStream); err != nil {
		slog.Error("invalid native writer projection stream configuration", "err", err)
		os.Exit(1)
	}
	if !roles["writer"] && commandWorkerMode != "" && !commandLogOneShot {
		slog.Error("command-log worker backend requires the writer role", "backend", commandWorkerMode, "roles", *roleList)
		os.Exit(1)
	}
	if commandMode != "" && commandAuthoritativeMode != "" {
		slog.Error("command-log shadow and authoritative submit modes are mutually exclusive",
			"commandLogShadow", commandMode,
			"commandLogAuthoritative", commandAuthoritativeMode)
		os.Exit(1)
	}
	if roles["writer"] && commandWorkerMode != "" && commandLogWorkerOwnershipMode == "hash-assignment" {
		if len(commandLogWorkerGroupMemberIDs) == 0 {
			slog.Error("hash-assignment ownership requires group members", "flag", "-command-log-worker-group-members")
			os.Exit(1)
		}
		if !containsString(commandLogWorkerGroupMemberIDs, *commandLogWorkerID) {
			slog.Error("hash-assignment group members must include this writer id",
				"ownerID", *commandLogWorkerID,
				"groupMembers", strings.Join(commandLogWorkerGroupMemberIDs, ","))
			os.Exit(1)
		}
	}
	if len(commandLogWorkerOverrides) > 0 {
		if commandWorkerMode == "" || !roles["writer"] {
			slog.Error("command-log assignment overrides require a writer command-log worker", "flag", "-command-log-worker-assignment-overrides")
			os.Exit(1)
		}
		if commandLogWorkerOwnershipMode == "sql-lease" {
			slog.Error("command-log assignment overrides require hash-assignment or nats-kv ownership",
				"flag", "-command-log-worker-assignment-overrides",
				"ownership", commandLogWorkerOwnershipMode)
			os.Exit(1)
		}
		for partition, ownerID := range commandLogWorkerOverrides {
			if !containsString(commandLogWorkerGroupMemberIDs, ownerID) {
				slog.Error("command-log assignment override owner is not in the worker group",
					"partitionKind", partition.Kind,
					"partitionKey", partition.Key,
					"ownerID", ownerID,
					"groupMembers", strings.Join(commandLogWorkerGroupMemberIDs, ","))
				os.Exit(1)
			}
		}
	}
	if roles["writer"] && commandWorkerMode != "" && commandLogWorkerOwnershipMode == "nats-kv" {
		if *natsURL == "" {
			slog.Error("nats-kv ownership requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		if len(commandLogWorkerGroupMemberIDs) == 0 {
			slog.Error("nats-kv ownership requires group members", "flag", "-command-log-worker-group-members")
			os.Exit(1)
		}
		if !containsString(commandLogWorkerGroupMemberIDs, *commandLogWorkerID) {
			slog.Error("nats-kv group members must include this writer id",
				"ownerID", *commandLogWorkerID,
				"groupMembers", strings.Join(commandLogWorkerGroupMemberIDs, ","))
			os.Exit(1)
		}
	}
	if roles["writer"] && commandWorkerMode != "" && commandAuthoritativeMode != "" && hasLocalCommandProducerRole(roles, *autoStats, *counterCheckpointInterval > 0) {
		slog.Error("command-log writer should not run in the same process that accepts authoritative command submissions",
			"commandLogAuthoritative", commandAuthoritativeMode,
			"commandLogWorker", commandWorkerMode,
			"roles", *roleList,
			"hint", "run public API/SSH nodes with -command-log-authoritative, and dedicated writer nodes with -command-log-worker")
		os.Exit(1)
	}
	if roles["writer"] && commandWorkerMode != "" && commandMode != "" && hasLocalCommandProducerRole(roles, *autoStats, *counterCheckpointInterval > 0) {
		slog.Error("command-log worker cannot run in the same process that shadows locally produced commands",
			"commandLogShadow", commandMode,
			"commandLogWorker", commandWorkerMode,
			"roles", *roleList,
			"hint", "run dedicated writer nodes, or disable command-log-shadow on this process")
		os.Exit(1)
	}
	if *natsURL != "" {
		if *storage != "postgres" {
			slog.Error("NATS cross-node delivery requires postgres storage", "flag", "-nats")
			os.Exit(1)
		}
		natsBroker, err = natsconn.Dial(*natsURL)
		if err != nil {
			slog.Error("NATS connection failed", "url", *natsURL, "err", err)
			os.Exit(1)
		}
		defer natsBroker.Close()
	}
	var kvCounterStore *natsconn.JetStreamCounterStore
	if counterStoreMode == "nats-kv" {
		kvCounterStore, err = natsconn.NewJetStreamCounterStore(context.Background(), natsBroker, natsconn.JetStreamCounterStoreOptions{
			Bucket:   *counterStoreNATSBucket,
			Shards:   *counterStoreShards,
			Replicas: *counterStoreNATSReplicas,
		})
		if err != nil {
			slog.Error("NATS KV counter store init failed", "bucket", *counterStoreNATSBucket, "err", err)
			os.Exit(1)
		}
		coreOptions = append(coreOptions, core.WithCounterStore(kvCounterStore))
		slog.Info("counter store enabled", "backend", counterStoreMode, "bucket", *counterStoreNATSBucket, "shards", *counterStoreShards)
	}
	if *repairCounterStoreAggregates {
		result, err := kvCounterStore.RebuildAggregateShardsFromIdentity(time.Now().UnixMilli())
		if err != nil {
			slog.Error("NATS KV counter aggregate repair failed", "bucket", *counterStoreNATSBucket, "err", err)
			os.Exit(1)
		}
		slog.Info("NATS KV counter aggregate repair complete",
			"bucket", *counterStoreNATSBucket,
			"reactionIdentityRecords", result.ReactionIdentityRecords,
			"reactionShardRecords", result.ReactionShardRecords,
			"pollVoteIdentityRecords", result.PollVoteIdentityRecords,
			"pollVoteShardRecords", result.PollVoteShardRecords,
			"deletedShardRecords", result.DeletedShardRecords)
		return
	}
	if presenceStoreMode == "nats-kv" {
		presenceStore, err := natsconn.NewJetStreamPresenceStore(context.Background(), natsBroker, natsconn.JetStreamPresenceStoreOptions{
			Bucket:   *presenceStoreNATSBucket,
			Replicas: *presenceStoreNATSReplicas,
			TTL:      *presenceStoreNATSTTL,
		})
		if err != nil {
			slog.Error("NATS KV presence store init failed", "bucket", *presenceStoreNATSBucket, "err", err)
			os.Exit(1)
		}
		coreOptions = append(coreOptions, core.WithPresenceStore(presenceStore))
		slog.Info("presence store enabled", "backend", presenceStoreMode, "bucket", *presenceStoreNATSBucket, "ttl", presenceStoreNATSTTL.String())
	}
	if chatStoreMode == "nats-kv" {
		chatStore, err := natsconn.NewJetStreamChatStore(context.Background(), natsBroker, natsconn.JetStreamChatStoreOptions{
			Bucket:   *chatStoreNATSBucket,
			Replicas: *chatStoreNATSReplicas,
		})
		if err != nil {
			slog.Error("NATS KV chat store init failed", "bucket", *chatStoreNATSBucket, "err", err)
			os.Exit(1)
		}
		coreOptions = append(coreOptions, core.WithChatStore(chatStore))
		slog.Info("chat store enabled", "backend", chatStoreMode, "bucket", *chatStoreNATSBucket)
	}
	readCache, readCacheCleanup, err := openReadCache(context.Background(), readCacheMode, *redisURL, *readCachePrefix, *readCacheTTL)
	if err != nil {
		slog.Error("read cache init failed", "backend", readCacheMode, "err", err)
		os.Exit(1)
	}
	defer readCacheCleanup()
	if readCache != nil {
		coreOptions = append(coreOptions, core.WithReadCache(readCache))
		slog.Info("read cache enabled", "backend", readCacheMode, "prefix", *readCachePrefix, "ttl", readCacheTTL.String())
	}
	if commandMode == "kafka" || commandAuthoritativeMode == "kafka" || commandWorkerMode == "kafka" {
		commandLogIndex = core.NewSQLCommandLogPartitionIndex(nil)
	}
	if commandMode == "" {
		commandMode = ""
	} else if commandMode == "memory" {
		shadowLog := core.NewMemoryCommandLog()
		coreOptions = append(coreOptions, core.WithCommandLogShadow(shadowLog))
		commandLogMetrics = shadowLog
	} else if commandMode == "nats" {
		if natsBroker == nil {
			slog.Error("NATS command-log shadow requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		shadowLog, err := natsconn.NewJetStreamCommandLog(context.Background(), natsBroker, natsconn.JetStreamCommandLogOptions{
			Stream:   *commandLogShadowNATSStream,
			Replicas: *commandLogShadowNATSReplicas,
		})
		if err != nil {
			slog.Error("NATS command-log shadow init failed", "stream", *commandLogShadowNATSStream, "err", err)
			os.Exit(1)
		}
		commandMode = "nats"
		coreOptions = append(coreOptions, core.WithCommandLogShadow(shadowLog))
		commandLogMetrics = shadowLog
	} else if commandMode == "kafka" {
		shadowLog, cleanup, err := openKafkaCommandLog(context.Background(), kafkaConfig, kafkaCommandPartitionCount, fmt.Sprintf("budgie-command-shadow-%d", os.Getpid()), commandLogIndex)
		if err != nil {
			slog.Error("Kafka command-log shadow init failed", "topic", kafkaConfig.Normalize().CommandTopic, "err", err)
			os.Exit(1)
		}
		defer cleanup()
		coreOptions = append(coreOptions, core.WithCommandLogShadow(shadowLog))
		commandLogMetrics = shadowLog
	} else {
		slog.Error("unsupported command log shadow backend", "backend", *commandLogShadow, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if commandAuthoritativeMode == "" {
		commandAuthoritativeMode = ""
	} else if commandAuthoritativeMode == "memory" {
		commandSubmitLog = core.NewMemoryCommandLog()
		coreOptions = append(coreOptions, core.WithAuthoritativeCommandLog(commandSubmitLog))
		commandLogMetrics = commandSubmitLog
	} else if commandAuthoritativeMode == "nats" {
		if natsBroker == nil {
			slog.Error("NATS authoritative command log requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		commandSubmitLog, err = natsconn.NewJetStreamCommandLog(context.Background(), natsBroker, natsconn.JetStreamCommandLogOptions{
			Stream:   *commandLogAuthoritativeNATSStream,
			Replicas: *commandLogAuthoritativeNATSReplicas,
		})
		if err != nil {
			slog.Error("NATS authoritative command-log init failed", "stream", *commandLogAuthoritativeNATSStream, "err", err)
			os.Exit(1)
		}
		commandAuthoritativeMode = "nats"
		coreOptions = append(coreOptions, core.WithAuthoritativeCommandLog(commandSubmitLog))
		commandLogMetrics = commandSubmitLog
	} else if commandAuthoritativeMode == "kafka" {
		var cleanup func()
		commandSubmitLog, cleanup, err = openKafkaCommandLog(context.Background(), kafkaConfig, kafkaCommandPartitionCount, fmt.Sprintf("budgie-command-authoritative-%d", os.Getpid()), commandLogIndex)
		if err != nil {
			slog.Error("Kafka authoritative command-log init failed", "topic", kafkaConfig.Normalize().CommandTopic, "err", err)
			os.Exit(1)
		}
		defer cleanup()
		coreOptions = append(coreOptions, core.WithAuthoritativeCommandLog(commandSubmitLog))
		commandLogMetrics = commandSubmitLog
	} else {
		slog.Error("unsupported authoritative command log backend", "backend", *commandLogAuthoritative, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if commandWorkerMode == "" {
		commandWorkerMode = ""
	} else if commandWorkerMode == "memory" {
		if commandAuthoritativeMode == "memory" && commandSubmitLog != nil {
			commandLog = commandSubmitLog
		} else {
			commandLog = core.NewMemoryCommandLog()
		}
		commandLogMetrics = commandLog
	} else if commandWorkerMode == "nats" {
		if natsBroker == nil {
			slog.Error("NATS command-log worker requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		if commandAuthoritativeMode == "nats" && commandSubmitLog != nil && *commandLogAuthoritativeNATSStream == *commandLogWorkerNATSStream {
			commandLog = commandSubmitLog
		} else {
			commandLog, err = natsconn.NewJetStreamCommandLog(context.Background(), natsBroker, natsconn.JetStreamCommandLogOptions{
				Stream:   *commandLogWorkerNATSStream,
				Replicas: *commandLogWorkerNATSReplicas,
				ReadOnly: commandLogOneShot,
			})
			if err != nil {
				slog.Error("NATS command-log worker init failed", "stream", *commandLogWorkerNATSStream, "err", err)
				os.Exit(1)
			}
		}
		commandWorkerMode = "nats"
		commandLogMetrics = commandLog
	} else if commandWorkerMode == "kafka" {
		if commandLogWorkerExecutorMode == "native" {
			var cleanup func()
			commandLog, kafkaNativeTx, cleanup, err = openKafkaNativeCommandLog(context.Background(), kafkaConfig, kafkaCommandPartitionCount, kafkaEventPartitionCount, *commandLogWorkerID, commandLogIndex)
			if err != nil {
				slog.Error("Kafka native command-log worker init failed", "topic", kafkaConfig.Normalize().CommandTopic, "eventTopic", kafkaConfig.Normalize().EventTopic, "err", err)
				os.Exit(1)
			}
			defer cleanup()
		} else if commandAuthoritativeMode == "kafka" && commandSubmitLog != nil {
			commandLog = commandSubmitLog
		} else {
			var cleanup func()
			commandLog, cleanup, err = openKafkaCommandLog(context.Background(), kafkaConfig, kafkaCommandPartitionCount, *commandLogWorkerID, commandLogIndex)
			if err != nil {
				slog.Error("Kafka command-log worker init failed", "topic", kafkaConfig.Normalize().CommandTopic, "err", err)
				os.Exit(1)
			}
			defer cleanup()
		}
		commandLogMetrics = commandLog
	} else {
		slog.Error("unsupported command log worker backend", "backend", *commandLogWorker, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if shadowMode == "" {
		shadowMode = ""
	} else if shadowMode == "memory" {
		coreOptions = append(coreOptions, core.WithEventLogShadow(core.EventLogShadowOptions{
			Shadow:         core.NewMemoryEventStore(),
			Interval:       *eventLogShadowInterval,
			ReplayLimit:    *eventLogShadowReplayLimit,
			PartitionLimit: *eventLogShadowPartitionLimit,
			StartAtHead:    *eventLogShadowStartHead,
		}))
	} else if shadowMode == "nats" {
		if natsBroker == nil {
			slog.Error("NATS event-log shadow requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		shadow, err := natsconn.NewJetStreamEventStore(context.Background(), natsBroker, natsconn.JetStreamEventLogOptions{
			Stream:   *eventLogShadowNATSStream,
			Replicas: *eventLogShadowNATSReplicas,
		})
		if err != nil {
			slog.Error("NATS event-log shadow init failed", "stream", *eventLogShadowNATSStream, "err", err)
			os.Exit(1)
		}
		shadowMode = "nats"
		coreOptions = append(coreOptions, core.WithEventLogShadow(core.EventLogShadowOptions{
			Shadow:         shadow,
			Interval:       *eventLogShadowInterval,
			ReplayLimit:    *eventLogShadowReplayLimit,
			PartitionLimit: *eventLogShadowPartitionLimit,
			StartAtHead:    *eventLogShadowStartHead,
		}))
	} else if shadowMode == "kafka" {
		var cleanup func()
		shadow, cleanup, err := openKafkaEventShadowStore(context.Background(), kafkaConfig, kafkaEventPartitionCount, fmt.Sprintf("budgie-event-shadow-%d", os.Getpid()))
		if err != nil {
			slog.Error("Kafka event-log shadow init failed", "topic", kafkaConfig.Normalize().EventTopic, "err", err)
			os.Exit(1)
		}
		defer cleanup()
		coreOptions = append(coreOptions, core.WithEventLogShadow(core.EventLogShadowOptions{
			Shadow:         shadow,
			Interval:       *eventLogShadowInterval,
			ReplayLimit:    *eventLogShadowReplayLimit,
			PartitionLimit: *eventLogShadowPartitionLimit,
			StartAtHead:    *eventLogShadowStartHead,
		}))
	} else {
		slog.Error("unsupported event log shadow backend", "backend", *eventLogShadow, "supported", "off,memory,nats,kafka")
		os.Exit(1)
	}
	if eventStoreProjectionMode == "nats" {
		if natsBroker == nil {
			slog.Error("NATS event-store projection requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
			os.Exit(1)
		}
		eventProjection, err = natsconn.NewJetStreamEventStore(context.Background(), natsBroker, natsconn.JetStreamEventLogOptions{
			Stream:   *eventStoreProjectionNATSStream,
			Replicas: *eventStoreProjectionNATSReplicas,
			ReadOnly: true,
		})
		if err != nil {
			slog.Error("NATS event-store projection source init failed", "stream", *eventStoreProjectionNATSStream, "err", err)
			os.Exit(1)
		}
	}
	searchIndexOptions, err := postSearchIndexOptions(
		postSearchIndexMode,
		*meiliSearchURL,
		*meiliSearchAPIKey,
		*meiliSearchIndex,
		*meiliSearchTaskTimeout,
		*meiliSearchPollInterval,
	)
	if err != nil {
		slog.Error("post search index init failed", "backend", postSearchIndexMode, "err", err)
		os.Exit(1)
	}
	coreOptions = append(coreOptions, searchIndexOptions...)
	if *asyncPostSearch || postSearchIndexMode != "sql-fts" {
		coreOptions = append(coreOptions, core.WithAsyncPostSearch())
	}
	if *asyncCommunityStatHistory {
		coreOptions = append(coreOptions, core.WithAsyncCommunityStatHistory())
	}
	switch *storage {
	case "postgres":
		if *pgDSN == "" {
			slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
			os.Exit(1)
		}
		slog.Info("starting budgied", "storage", "postgres", "dsn", obfuscateDSN(*pgDSN))
		if natsBroker != nil {
			coreOptions = append(coreOptions, core.WithBusFactory(func(nodeID string) core.Bus {
				return core.NewNATSBus(natsBroker, nodeID)
			}, true))
		}
		c, err = core.NewPostgres(*pgDSN, coreOptions...)
	default:
		slog.Info("starting budgied", "storage", "sqlite", "db", *dbPath)
		c, err = core.New(*dbPath, coreOptions...)
	}
	if err != nil {
		slog.Error("core init failed", "err", err)
		os.Exit(1)
	}
	if commandLogIndex != nil {
		commandLogIndex.BindDB(c.DB)
	}
	if eventStoreProjectionMode == "kafka" {
		var cleanup func()
		eventProjection, cleanup, err = openKafkaEventProjectionStore(context.Background(), kafkaConfig, kafkaEventPartitionCount, fmt.Sprintf("budgie-event-projection-%d", os.Getpid()), c.DB)
		if err != nil {
			slog.Error("Kafka event-store projection source init failed", "topic", kafkaConfig.Normalize().EventTopic, "err", err)
			os.Exit(1)
		}
		defer cleanup()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *checkCommandLogPromotionReadiness {
		if commandLog == nil {
			slog.Error("command-log promotion readiness check has no command log", "flag", "-command-log-worker")
			os.Exit(1)
		}
		report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, core.CommandLogPromotionReadinessConfig{
			PartitionLimit: *commandLogWorkerLimit,
			BatchSize:      *commandLogWorkerBatchSize,
		})
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if printErr := encoder.Encode(report); printErr != nil {
			slog.Error("command-log promotion readiness report failed", "err", printErr)
			os.Exit(1)
		}
		if err != nil {
			slog.Error("command-log promotion readiness check failed", "err", err)
			os.Exit(1)
		}
		if !report.Ready {
			slog.Error("command-log promotion readiness check failed",
				"laggingPartitions", report.LaggingPartitions,
				"totalLag", report.TotalLag,
				"missingMaterialization", report.MaterializationAudit.MissingMaterialization,
				"retryingCommitted", report.MaterializationAudit.RetryingCommitted,
				"missingRecords", report.MaterializationAudit.MissingRecords)
			os.Exit(1)
		}
		slog.Info("command-log promotion readiness check passed",
			"partitions", report.Partitions,
			"tailCommands", report.TailCommands,
			"committedCommands", report.CommittedCommands)
		return
	}

	if *auditCommandLogMaterialization {
		if commandLog == nil {
			slog.Error("command-log materialization audit has no command log", "flag", "-command-log-worker")
			os.Exit(1)
		}
		report, err := c.AuditCommandLogMaterialization(ctx, commandLog, core.CommandLogMaterializationAuditConfig{
			PartitionLimit: *commandLogWorkerLimit,
			BatchSize:      *commandLogWorkerBatchSize,
		})
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if printErr := encoder.Encode(report); printErr != nil {
			slog.Error("command-log materialization audit report failed", "err", printErr)
			os.Exit(1)
		}
		if err != nil {
			slog.Error("command-log materialization audit failed", "err", err)
			os.Exit(1)
		}
		if !report.Complete {
			slog.Error("command-log materialization audit incomplete",
				"missingMaterialization", report.MissingMaterialization,
				"retryingCommitted", report.RetryingCommitted,
				"missingRecords", report.MissingRecords)
			os.Exit(1)
		}
		slog.Info("command-log materialization audit complete",
			"partitions", report.Partitions,
			"committedCommands", report.CommittedCommands,
			"appliedCommands", report.AppliedCommands,
			"terminalFailures", report.TerminalFailures)
		return
	}

	// Signup captcha (off unless configured). Native mode reuses the JWT secret
	// as its HMAC key when no dedicated captcha secret is set, so challenge
	// hashes verify on any node.
	capSecret := envOr2(*captchaSecret, "BUDGIE_CAPTCHA_SECRET")
	if capSecret == "" {
		capSecret = string(secret)
	}
	c.SetCaptcha(core.CaptchaConfig{
		Mode:      envOr2(*captchaMode, "BUDGIE_CAPTCHA_MODE"),
		Provider:  envOr2(*captchaProvider, "BUDGIE_CAPTCHA_PROVIDER"),
		SiteKey:   envOr2(*captchaSiteKey, "BUDGIE_CAPTCHA_SITE_KEY"),
		Secret:    capSecret,
		VerifyURL: *captchaVerifyURL,
	})

	// Outbound email + verification (off unless a From address is configured).
	// Default transport is direct-to-MX; an SMTP relay (BUDGIE_SMTP_HOST) takes
	// over when set, which is also how an ESP is wired in.
	mailModeResolved := envOr2(*mailMode, "BUDGIE_MAIL_MODE")
	mailFromResolved := envOr2(*mailFromAddr, "BUDGIE_MAIL_FROM")
	smtpHostResolved := envOr2(*smtpHost, "BUDGIE_SMTP_HOST")
	if mailModeResolved == "" {
		switch {
		case mailFromResolved == "":
			mailModeResolved = mailer.ModeOff
		case smtpHostResolved != "":
			mailModeResolved = mailer.ModeRelay
		default:
			mailModeResolved = mailer.ModeDirect
		}
	}
	mailerInstance, err := mailer.New(mailer.Config{
		Mode:     mailModeResolved,
		From:     mailFromResolved,
		Host:     smtpHostResolved,
		Port:     *smtpPort,
		Username: envOr2(*smtpUser, "BUDGIE_SMTP_USER"),
		Password: envOr2(*smtpPassword, "BUDGIE_SMTP_PASSWORD"),
	})
	if err != nil {
		slog.Error("mailer configuration invalid", "err", err)
		os.Exit(1)
	}
	c.SetMailer(mailerInstance, mailFromResolved, *requireEmailVerify, envOr2(*publicURL, "BUDGIE_PUBLIC_URL"))
	// Classify the backend so verification defaults make sense per environment:
	//   no mailer        -> verification stays off (nothing can be sent)
	//   loopback relay   -> mailpit-style dev catcher; surface its web inbox
	//   remote relay/MX  -> real provider; verification enforced by default
	if mailerInstance == nil {
		if *requireEmailVerify {
			slog.Info("email verification requested but no mailer configured; verification disabled (set -mail-from to enable)")
		}
	} else {
		devInbox := envOr2(*mailInboxURL, "BUDGIE_MAIL_INBOX_URL")
		loopbackRelay := mailModeResolved == mailer.ModeRelay && isLoopbackHost(smtpHostResolved)
		if devInbox == "" && loopbackRelay {
			devInbox = "http://localhost:8025" // mailpit's default web inbox
		}
		c.SetMailDevInbox(devInbox)
		slog.Info("outbound email enabled", "mode", mailModeResolved, "from", mailFromResolved, "verification", c.EmailVerificationEnabled())
		if devInbox != "" {
			slog.Info("email: local SMTP catcher in use; view captured mail", "inbox", devInbox)
		}
	}

	// Register scrape-time metrics collectors (SSH sessions, outbox counts).
	c.RegisterMetricsCollectors()
	core.RegisterCommandLogMetricsCollector(commandLogMetrics, *commandLogWorkerLimit)
	var commandLogWorkerAssignments core.CommandPartitionAssigner
	var commandLogWorkerClaims core.CommandPartitionClaimer
	commandLogWorkerExecutorInstance := core.CommandLogExecutor(c)
	var commandLogWorkerFinalizer core.CommandLogFinalizer
	if roles["writer"] {
		switch commandLogWorkerOwnershipMode {
		case "sql-lease":
			commandLogWorkerClaims = core.NewSQLCommandPartitionClaimer(c.DB)
		case "hash-assignment":
			assigner := core.NewHashCommandPartitionAssignerWithOverrides(commandLogWorkerGroupMemberIDs, commandLogWorkerOverrides, 1)
			commandLogWorkerAssignments = assigner
			commandLogWorkerGroupMemberIDs = assigner.Members()
		case "nats-kv":
			assigner, err := natsconn.NewJetStreamCommandPartitionAssigner(ctx, natsBroker, natsconn.JetStreamCommandPartitionAssignerOptions{
				Bucket:    *commandLogWorkerAssignBucket,
				Group:     *commandLogWorkerNATSStream,
				Members:   commandLogWorkerGroupMemberIDs,
				Overrides: commandLogWorkerOverrides,
				Replicas:  *commandLogWorkerNATSReplicas,
			})
			if err != nil {
				slog.Error("NATS command-log worker assignment init failed", "err", err)
				os.Exit(1)
			}
			commandLogWorkerAssignments = assigner
			if members, generation, err := assigner.Members(ctx); err == nil {
				commandLogWorkerGroupMemberIDs = members
				slog.Info("command log writer assignment group ready",
					"ownership", commandLogWorkerOwnershipMode,
					"group", *commandLogWorkerNATSStream,
					"generation", generation,
					"groupMembers", strings.Join(commandLogWorkerGroupMemberIDs, ","))
			}
		}
	}
	if roles["writer"] && commandLogWorkerExecutorMode == "native" {
		var transactions core.CommandEventTransactionStore
		switch commandWorkerMode {
		case "nats":
			if natsBroker == nil {
				slog.Error("native command-log worker executor requires a NATS URL", "flag", "-nats", "env", "BUDGIE_NATS_URL")
				os.Exit(1)
			}
			transactions, err = natsconn.NewJetStreamCommandEventTransactionStore(ctx, natsBroker, natsconn.JetStreamCommandEventTransactionOptions{
				CommandLog: natsconn.JetStreamCommandLogOptions{
					Stream:   *commandLogWorkerNATSStream,
					Replicas: *commandLogWorkerNATSReplicas,
				},
				EventLog: natsconn.JetStreamEventLogOptions{
					Stream:   *commandLogWorkerEventNATSStream,
					Replicas: *commandLogWorkerEventNATSReplicas,
				},
			})
			if err != nil {
				slog.Error("NATS command/event transaction init failed",
					"commandStream", *commandLogWorkerNATSStream,
					"eventStream", *commandLogWorkerEventNATSStream,
					"err", err)
				os.Exit(1)
			}
		case "kafka":
			if kafkaNativeTx == nil {
				slog.Error("Kafka native command/event transaction was not initialized")
				os.Exit(1)
			}
			transactions, err = kafkaNativeTx(c.DB)
			if err != nil {
				slog.Error("Kafka command/event transaction init failed", "topic", kafkaConfig.Normalize().CommandTopic, "eventTopic", kafkaConfig.Normalize().EventTopic, "err", err)
				os.Exit(1)
			}
		default:
			slog.Error("unsupported native command/event transaction backend", "backend", commandWorkerMode)
			os.Exit(1)
		}
		nativeExecutor := core.NewCommandLogNativeDecisionExecutor(c)
		commandLogWorkerExecutorInstance = nativeExecutor
		commandLogWorkerFinalizer = core.CommandEventTransactionFinalizer{
			Transactions:      transactions,
			Events:            nativeExecutor,
			Applied:           c,
			TerminalFailures:  c,
			RetryableFailures: c,
		}
	}
	if commandLogWorkerAssignments != nil {
		if lister, ok := commandLog.(core.CommandPartitionLister); ok {
			core.RegisterCommandAssignmentMetricsCollector(commandLogWorkerAssignments, lister, *commandLogWorkerID, *commandLogWorkerLimit)
		}
	}
	if commandMode != "" {
		slog.Info("command log shadow enabled", "backend", commandMode)
	}
	if commandAuthoritativeMode != "" {
		slog.Info("authoritative command log submission enabled", "backend", commandAuthoritativeMode)
	}
	if shadowMode != "" {
		if runner := c.StartEventLogShadowParity(ctx); runner != nil {
			slog.Info("event log shadow parity enabled",
				"backend", shadowMode,
				"interval", eventLogShadowInterval.String(),
				"replayLimit", *eventLogShadowReplayLimit,
				"partitionLimit", *eventLogShadowPartitionLimit,
				"startAtHead", *eventLogShadowStartHead)
		}
	}
	if commandWorkerMode != "" {
		slog.Info("command log writer enabled",
			"backend", commandWorkerMode,
			"interval", commandLogWorkerInterval.String(),
			"batchSize", *commandLogWorkerBatchSize,
			"partitionConcurrency", *commandLogWorkerConcurrency,
			"partitionLimit", *commandLogWorkerLimit,
			"executor", commandLogWorkerExecutorMode,
			"ownership", commandLogWorkerOwnershipMode,
			"groupMembers", strings.Join(commandLogWorkerGroupMemberIDs, ","),
			"assignmentOverrides", len(commandLogWorkerOverrides),
			"claimTTL", commandLogWorkerClaimTTL.String(),
			"claimRefreshInterval", commandLogWorkerClaimRefreshInterval.String(),
			"ownerID", *commandLogWorkerID)
	}

	broker := "none"
	if *storage == "postgres" {
		broker = "postgres-listen-notify"
		if natsBroker != nil {
			broker = "nats"
		}
	}
	slog.Info("node configuration",
		"roles", *roleList, "storage", *storage, "broker", broker,
		"http", *httpAddr, "ssh", roleAddr(roles["ssh"], *sshPort),
		"nntp", roleAddr(roles["nntp"] && *nntpAddr != "", *nntpAddr),
		"writer", roleAddr(roles["writer"], commandWorkerMode))

	// The command handler and (in Postgres mode) the cross-node listener run on
	// every node regardless of role.
	go c.Run(ctx)

	// Command-log writer role: experimental IS4 path that drains broker-owned
	// command partitions through the current SQL-backed command executor.
	if roles["writer"] {
		worker := core.NewCommandLogWorker(core.CommandLogWorkerConfig{
			Log:                  commandLog,
			Assignments:          commandLogWorkerAssignments,
			Claims:               commandLogWorkerClaims,
			Executor:             commandLogWorkerExecutorInstance,
			Finalizer:            commandLogWorkerFinalizer,
			OwnerID:              *commandLogWorkerID,
			BatchSize:            *commandLogWorkerBatchSize,
			PartitionLimit:       *commandLogWorkerLimit,
			PartitionConcurrency: *commandLogWorkerConcurrency,
			Interval:             *commandLogWorkerInterval,
			ClaimTTL:             *commandLogWorkerClaimTTL,
			ClaimRefreshInterval: commandLogWorkerClaimRefreshInterval,
		})
		go runCommandLogWorker(ctx, worker, commandWorkerMode, *commandLogWorkerInterval)
	}
	if eventStoreProjectionMode != "" {
		if eventProjection == nil {
			slog.Error("event-store projection worker has no event store", "backend", eventStoreProjectionMode)
			os.Exit(1)
		}
		if _, err := c.StartEventStoreProjectionWorker(ctx, core.EventStoreProjectionWorkerConfig{
			Store:          eventProjection,
			Source:         eventStoreProjectionSourceName,
			BatchSize:      *eventStoreProjectionBatchSize,
			PartitionLimit: *eventStoreProjectionPartitionLimit,
			Interval:       *eventStoreProjectionInterval,
		}); err != nil {
			slog.Error("event-store projection worker failed to start", "backend", eventStoreProjectionMode, "err", err)
			os.Exit(1)
		}
		slog.Info("event-store projection worker enabled",
			"backend", eventStoreProjectionMode,
			"stream", *eventStoreProjectionNATSStream,
			"topic", kafkaConfig.Normalize().EventTopic,
			"source", eventStoreProjectionSourceName,
			"interval", eventStoreProjectionInterval.String(),
			"batchSize", *eventStoreProjectionBatchSize,
			"partitionLimit", *eventStoreProjectionPartitionLimit)
	}

	// Worker role: background jobs (outbox + stats), leader-elected in Postgres.
	if roles["worker"] {
		c.StartBackgroundWorkerWithConfig(ctx, core.BackgroundWorkerConfig{
			AutoStats:                 *autoStats,
			CounterCheckpointInterval: *counterCheckpointInterval,
		})
		if *counterCheckpointInterval > 0 {
			slog.Info("counter checkpoint scheduler enabled", "interval", counterCheckpointInterval.String())
		}
	}
	if len(derivedViewWatermarkViews) > 0 {
		if _, err := c.StartDerivedViewWatermarkWorker(ctx, derivedViewWatermarkViews, *derivedViewWatermarkInterval); err != nil {
			slog.Error("derived view watermark worker failed to start", "views", *derivedViewWatermarks, "err", err)
			os.Exit(1)
		}
		slog.Info("derived view watermark worker enabled",
			"views", strings.Join(derivedViewWatermarkViews, ","),
			"interval", derivedViewWatermarkInterval.String())
	}
	if len(derivedViewProcessorViews) > 0 {
		slog.Info("derived view processors selected",
			"views", strings.Join(derivedViewProcessorViews, ","))
	}
	if *postSearchProcessor {
		if _, err := c.StartPostSearchProcessor(ctx, *postSearchProcessorInterval, *postSearchProcessorBatchSize); err != nil {
			slog.Error("post search processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("post search processor enabled",
			"asyncPostSearch", *asyncPostSearch,
			"interval", postSearchProcessorInterval.String(),
			"batchSize", *postSearchProcessorBatchSize)
	}
	if *digestSearchProcessor {
		if _, err := c.StartDigestSearchProcessor(ctx, *digestSearchProcessorInterval, *digestSearchProcessorBatchSize); err != nil {
			slog.Error("digest search processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("digest search processor enabled",
			"interval", digestSearchProcessorInterval.String(),
			"batchSize", *digestSearchProcessorBatchSize)
	}
	if *communityStatsProcessor {
		if _, err := c.StartCommunityStatsProcessor(ctx, *communityStatsProcessorInterval, *communityStatsProcessorBatchSize); err != nil {
			slog.Error("community stats processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("community stats processor enabled",
			"interval", communityStatsProcessorInterval.String(),
			"batchSize", *communityStatsProcessorBatchSize)
	}
	if *latestFeedProcessor {
		if _, err := c.StartLatestFeedProcessor(ctx, *latestFeedProcessorInterval, *latestFeedProcessorBatchSize); err != nil {
			slog.Error("latest feed processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("latest feed processor enabled",
			"interval", latestFeedProcessorInterval.String(),
			"batchSize", *latestFeedProcessorBatchSize)
	}
	if *residentFeedProcessor {
		if _, err := c.StartResidentFeedProcessor(ctx, *residentFeedProcessorInterval, *residentFeedProcessorBatchSize); err != nil {
			slog.Error("resident feed processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("resident feed processor enabled",
			"interval", residentFeedProcessorInterval.String(),
			"batchSize", *residentFeedProcessorBatchSize)
	}
	if *boardSummariesProcessor {
		if _, err := c.StartBoardSummariesProcessor(ctx, *boardSummariesProcessorInterval, *boardSummariesProcessorBatchSize); err != nil {
			slog.Error("board summaries processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("board summaries processor enabled",
			"interval", boardSummariesProcessorInterval.String(),
			"batchSize", *boardSummariesProcessorBatchSize)
	}
	if *unreadThreadSummariesProcessor {
		if _, err := c.StartUnreadThreadSummariesProcessor(ctx, *unreadThreadSummariesInterval, *unreadThreadSummariesBatchSize); err != nil {
			slog.Error("unread thread summaries processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("unread thread summaries processor enabled",
			"interval", unreadThreadSummariesInterval.String(),
			"batchSize", *unreadThreadSummariesBatchSize)
	}
	if *boardRankingsProcessor {
		if _, err := c.StartBoardRankingsProcessor(ctx, *boardRankingsProcessorInterval, *boardRankingsProcessorBatchSize); err != nil {
			slog.Error("board rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("board rankings processor enabled",
			"interval", boardRankingsProcessorInterval.String(),
			"batchSize", *boardRankingsProcessorBatchSize)
	}
	if *threadRankingsProcessor {
		if _, err := c.StartThreadRankingsProcessor(ctx, *threadRankingsProcessorInterval, *threadRankingsProcessorBatchSize); err != nil {
			slog.Error("thread rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("thread rankings processor enabled",
			"interval", threadRankingsProcessorInterval.String(),
			"batchSize", *threadRankingsProcessorBatchSize)
	}
	if *replyRankingsProcessor {
		if _, err := c.StartReplyRankingsProcessor(ctx, *replyRankingsProcessorInterval, *replyRankingsProcessorBatchSize); err != nil {
			slog.Error("reply rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("reply rankings processor enabled",
			"interval", replyRankingsProcessorInterval.String(),
			"batchSize", *replyRankingsProcessorBatchSize)
	}
	if *userRankingsProcessor {
		if _, err := c.StartUserRankingsProcessor(ctx, *userRankingsProcessorInterval, *userRankingsProcessorBatchSize); err != nil {
			slog.Error("user rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("user rankings processor enabled",
			"interval", userRankingsProcessorInterval.String(),
			"batchSize", *userRankingsProcessorBatchSize)
	}
	if *blessingRankingsProcessor {
		if _, err := c.StartBlessingRankingsProcessor(ctx, *blessingRankingsProcessorInterval, *blessingRankingsProcessorBatchSize); err != nil {
			slog.Error("blessing rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("blessing rankings processor enabled",
			"interval", blessingRankingsProcessorInterval.String(),
			"batchSize", *blessingRankingsProcessorBatchSize)
	}
	if *archiveRankingsProcessor {
		if _, err := c.StartArchiveRankingsProcessor(ctx, *archiveRankingsProcessorInterval, *archiveRankingsProcessorBatchSize); err != nil {
			slog.Error("archive rankings processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("archive rankings processor enabled",
			"interval", archiveRankingsProcessorInterval.String(),
			"batchSize", *archiveRankingsProcessorBatchSize)
	}

	// HTTP listener — always started so /healthz, /readyz, /metrics are reachable
	// on every node. The full API/WS/SPA is mounted only on the api role; the
	// gateway role gets live WS/SSE/poll transports without the REST API surface.
	httpSrv := httpapi.New(c, secret)
	if err := httpSrv.SetWriteRegionURL(*writeRegionURL); err != nil {
		slog.Error("invalid write region URL", "flag", "-write-region-url", "value", *writeRegionURL, "err", err)
		os.Exit(1)
	}
	if root := resolveWebRoot(*webRoot); root != "" {
		httpSrv.SetWebRoot(root)
	}
	mux := http.NewServeMux()
	writeRegionProxyEnabled := strings.TrimSpace(*writeRegionURL) != ""
	wsAllowCommands := !writeRegionProxyEnabled || commandAuthoritativeMode != ""
	if roles["api"] {
		wsSrv := wsapi.NewGateway(c, secret, wsAllowCommands)
		mux.Handle("/api/v1/ws", wsSrv)
		mux.Handle("/", httpSrv.Handler())
	} else if roles["gateway"] {
		wsSrv := wsapi.NewGateway(c, secret, commandAuthoritativeMode != "")
		mux.Handle("/api/v1/ws", wsSrv)
		mux.Handle("/", httpSrv.GatewayHandler())
	} else {
		mux.Handle("/", httpSrv.OpsHandler())
	}
	srv := &http.Server{Addr: *httpAddr, Handler: mux}
	go func() {
		slog.Info("HTTP listening", "addr", *httpAddr, "api", roles["api"], "gateway", roles["gateway"], "writeRegionProxy", writeRegionProxyEnabled, "wsCommands", wsAllowCommands)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// SSH TUI server (ssh role).
	if roles["ssh"] {
		hk := hostKeyPath(*hostKey)
		ensureHostKey(hk)
		tuiSrv := tui.New(c, *sshPort, hk)
		if *doorsConf != "" {
			doors, err := core.LoadDoorsConfig(*doorsConf)
			if err != nil {
				slog.Error("doors config load failed", "path", *doorsConf, "err", err)
				os.Exit(1)
			}
			if doors != nil {
				tuiSrv.SetDoors(doors)
				slog.Info("doors loaded", "count", len(doors.Doors), "path", *doorsConf)
			}
		}
		go func() {
			if err := tuiSrv.ListenAndServe(ctx); err != nil {
				slog.Error("SSH server error", "err", err)
			}
		}()
	}

	// NNTP gateway (nntp role, only when an address is configured).
	if roles["nntp"] && *nntpAddr != "" {
		nntpSrv := nntp.New(c, *nntpAddr, *nntpDomain, *nntpPrefix)
		go func() {
			if err := nntpSrv.ListenAndServe(ctx); err != nil {
				slog.Error("NNTP server error", "err", err)
			}
		}()
	}

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envOr2 prefers a non-empty flag value, falling back to an env var.
func envOr2(flagVal, envKey string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envKey)
}

// isLoopbackHost reports whether host names the local machine — the marker of a
// dev SMTP catcher (mailpit) rather than a real provider/relay.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func defaultCommandLogWorkerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func effectiveCommandLogWorkerClaimRefreshInterval(claimTTL, configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	if claimTTL <= 0 {
		claimTTL = 30 * time.Second
	}
	interval := claimTTL / 3
	if interval <= 0 {
		return time.Second
	}
	return interval
}

type derivedViewProcessorFlags struct {
	asyncPostSearch                *bool
	postSearchProcessor            *bool
	digestSearchProcessor          *bool
	communityStatsProcessor        *bool
	asyncCommunityStatHistory      *bool
	latestFeedProcessor            *bool
	residentFeedProcessor          *bool
	boardSummariesProcessor        *bool
	unreadThreadSummariesProcessor *bool
	boardRankingsProcessor         *bool
	threadRankingsProcessor        *bool
	replyRankingsProcessor         *bool
	userRankingsProcessor          *bool
	blessingRankingsProcessor      *bool
	archiveRankingsProcessor       *bool
}

func applyDerivedViewProcessorSelection(raw string, flags derivedViewProcessorFlags) ([]string, error) {
	resolved, err := core.ResolveDerivedViews([]string{raw})
	if err != nil {
		return nil, err
	}
	for _, view := range resolved {
		switch view {
		case core.DerivedViewPostSearch:
			setBool(flags.asyncPostSearch, true)
			setBool(flags.postSearchProcessor, true)
		case core.DerivedViewDigestSearch:
			setBool(flags.digestSearchProcessor, true)
		case core.DerivedViewCommunityStats:
			setBool(flags.communityStatsProcessor, true)
		case core.DerivedViewCommunityStatHistory:
			setBool(flags.asyncCommunityStatHistory, true)
		case core.DerivedViewLatestFeed:
			setBool(flags.latestFeedProcessor, true)
		case core.DerivedViewResidentFeed:
			setBool(flags.residentFeedProcessor, true)
		case core.DerivedViewBoardSummaries:
			setBool(flags.boardSummariesProcessor, true)
		case core.DerivedViewUnreadThreads:
			setBool(flags.unreadThreadSummariesProcessor, true)
		case core.DerivedViewBoardRankings:
			setBool(flags.boardRankingsProcessor, true)
		case core.DerivedViewThreadRankings:
			setBool(flags.threadRankingsProcessor, true)
		case core.DerivedViewReplyRankings:
			setBool(flags.replyRankingsProcessor, true)
		case core.DerivedViewUserRankings:
			setBool(flags.userRankingsProcessor, true)
		case core.DerivedViewBlessingRankings:
			setBool(flags.blessingRankingsProcessor, true)
		case core.DerivedViewArchiveRankings:
			setBool(flags.archiveRankingsProcessor, true)
		}
	}
	return resolved, nil
}

func setBool(target *bool, value bool) {
	if target != nil {
		*target = value
	}
}

// parseRoles turns a comma-separated role list into a set, exiting on an
// unknown role. Known roles: api, gateway, ssh, worker, nntp, writer.
func parseRoles(list string) map[string]bool {
	known := map[string]bool{"api": true, "gateway": true, "ssh": true, "worker": true, "nntp": true, "writer": true}
	roles := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		r := strings.ToLower(strings.TrimSpace(raw))
		if r == "" {
			continue
		}
		if !known[r] {
			slog.Error("unknown role", "role", r, "known", "api,gateway,ssh,worker,nntp,writer")
			os.Exit(1)
		}
		roles[r] = true
	}
	if len(roles) == 0 {
		slog.Error("at least one role is required", "flag", "-roles")
		os.Exit(1)
	}
	return roles
}

func normalizeLogBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "off", "none":
		return ""
	case "jetstream":
		return "nats"
	case "kafka", "redpanda":
		return "kafka"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func validateKafkaCommandLogBackend(mode string, config kafkaconn.RuntimeConfig, partitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateCommandLog(); err != nil {
		return err
	}
	if partitions <= 0 {
		return fmt.Errorf("kafka command-log backend requires -kafka-command-partitions for logical partition mapping")
	}
	return nil
}

func validateKafkaEventShadowBackend(mode string, config kafkaconn.RuntimeConfig, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateEventLog(); err != nil {
		return err
	}
	if eventPartitions <= 0 {
		return fmt.Errorf("kafka event-log shadow requires -kafka-event-partitions for logical event-partition mapping")
	}
	return nil
}

func validateKafkaCommandWorkerBackend(mode, executorMode string, config kafkaconn.RuntimeConfig, commandPartitions, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if executorMode == "native" {
		if err := config.ValidateCommandEventTransaction(); err != nil {
			return err
		}
		if commandPartitions <= 0 {
			return fmt.Errorf("native Kafka command-log worker requires -kafka-command-partitions for logical command-partition mapping")
		}
		if eventPartitions <= 0 {
			return fmt.Errorf("native Kafka command-log worker requires -kafka-event-partitions for logical event-partition mapping")
		}
		return nil
	}
	return validateKafkaCommandLogBackend(mode, config, commandPartitions)
}

func validateKafkaEventProjectionBackend(mode string, config kafkaconn.RuntimeConfig, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateEventLog(); err != nil {
		return err
	}
	if eventPartitions <= 0 {
		return fmt.Errorf("kafka event-store projection requires -kafka-event-partitions for logical event-partition mapping")
	}
	return nil
}

func normalizeEventLogPromotionReadinessBackend(raw string) string {
	mode := normalizeLogBackend(raw)
	if mode == "" {
		return "nats"
	}
	return mode
}

func normalizeProjectionRebuildSource(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql":
		return "sql"
	case "nats", "jetstream":
		return "nats"
	case "kafka", "redpanda":
		return "kafka"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

type projectionRebuildEventStoreOptions struct {
	Source               string
	NATSURL              string
	NATSStream           string
	NATSReplicas         int
	KafkaConfig          kafkaconn.RuntimeConfig
	KafkaEventPartitions int32
	ClientID             string
	DB                   *sql.DB
}

func openProjectionRebuildEventStore(ctx context.Context, options projectionRebuildEventStoreOptions) (string, core.EventStore, func(), error) {
	source := normalizeProjectionRebuildSource(options.Source)
	switch source {
	case "nats":
		if strings.TrimSpace(options.NATSURL) == "" {
			return "", nil, func() {}, fmt.Errorf("NATS projection rebuild source requires a NATS URL (-nats or BUDGIE_NATS_URL)")
		}
		natsBroker, err := natsconn.Dial(options.NATSURL)
		if err != nil {
			return "", nil, func() {}, fmt.Errorf("nats connection failed: %w", err)
		}
		store, err := natsconn.NewJetStreamEventStore(ctx, natsBroker, natsconn.JetStreamEventLogOptions{
			Stream:   options.NATSStream,
			Replicas: options.NATSReplicas,
			ReadOnly: true,
		})
		if err != nil {
			natsBroker.Close()
			return "", nil, func() {}, fmt.Errorf("nats projection rebuild source init failed: %w", err)
		}
		return source, store, func() {
			natsBroker.Close()
		}, nil
	case "kafka":
		clientID := strings.TrimSpace(options.ClientID)
		if clientID == "" {
			clientID = fmt.Sprintf("budgie-projection-rebuild-%d", os.Getpid())
		}
		store, cleanup, err := openKafkaEventProjectionStore(ctx, options.KafkaConfig, options.KafkaEventPartitions, clientID, options.DB)
		if err != nil {
			return "", nil, func() {}, fmt.Errorf("kafka projection rebuild source init failed: %w", err)
		}
		return source, store, cleanup, nil
	default:
		return "", nil, func() {}, fmt.Errorf("unsupported projection rebuild source %q (supported: sql,nats,kafka)", options.Source)
	}
}

func openEventLogPromotionReadinessStore(ctx context.Context, mode, natsURL, natsStream string, natsReplicas int, kafkaConfig kafkaconn.RuntimeConfig, kafkaEventPartitions int32, clientID string, db *sql.DB) (core.EventStore, func(), error) {
	mode = normalizeEventLogPromotionReadinessBackend(mode)
	switch mode {
	case "nats":
		if strings.TrimSpace(natsURL) == "" {
			return nil, func() {}, fmt.Errorf("event-log promotion readiness requires a NATS URL (-nats or BUDGIE_NATS_URL)")
		}
		natsBroker, err := natsconn.Dial(natsURL)
		if err != nil {
			return nil, func() {}, fmt.Errorf("nats connection failed: %w", err)
		}
		store, err := natsconn.NewJetStreamEventStore(ctx, natsBroker, natsconn.JetStreamEventLogOptions{
			Stream:   natsStream,
			Replicas: natsReplicas,
			ReadOnly: true,
		})
		if err != nil {
			natsBroker.Close()
			return nil, func() {}, fmt.Errorf("nats event-log promotion source init failed: %w", err)
		}
		return store, func() {
			natsBroker.Close()
		}, nil
	case "kafka":
		return openKafkaEventProjectionStore(ctx, kafkaConfig, kafkaEventPartitions, clientID, db)
	default:
		return nil, func() {}, fmt.Errorf("unsupported event-log promotion readiness backend %q (supported: nats,kafka)", mode)
	}
}

func openKafkaCommandLog(ctx context.Context, config kafkaconn.RuntimeConfig, partitions int32, clientID string, index core.CommandLogPartitionIndexer) (core.CommandLog, func(), error) {
	if err := validateKafkaCommandLogBackend("kafka", config, partitions); err != nil {
		return nil, func() {}, err
	}
	if index == nil {
		return nil, func() {}, fmt.Errorf("kafka command log requires a command partition index")
	}
	runtime := config.Normalize()
	client, err := kafkaconn.NewCommandLogRuntimeClient(ctx, kafkaconn.CommandLogRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
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
			PartitionCount: partitions,
			Candidates:     index,
		},
	)
	return core.NewIndexedCommandLog(log, index), cleanup, nil
}

type kafkaNativeTransactionBinder func(*sql.DB) (core.CommandEventTransactionStore, error)

func openKafkaNativeCommandLog(ctx context.Context, config kafkaconn.RuntimeConfig, commandPartitions, eventPartitions int32, clientID string, index core.CommandLogPartitionIndexer) (core.CommandLog, kafkaNativeTransactionBinder, func(), error) {
	if err := validateKafkaCommandWorkerBackend("kafka", "native", config, commandPartitions, eventPartitions); err != nil {
		return nil, nil, func() {}, err
	}
	if index == nil {
		return nil, nil, func() {}, fmt.Errorf("kafka command log requires a command partition index")
	}
	runtime := config.Normalize()
	commandProducerClient, err := kafkaconn.NewCommandLogProducerRuntimeClient(ctx, kafkaconn.CommandLogProducerRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID + "-command-producer",
	})
	if err != nil {
		return nil, nil, func() {}, err
	}
	var transactionPoolCleanup func()
	cleanup := func() {
		if transactionPoolCleanup != nil {
			transactionPoolCleanup()
		}
		commandProducerClient.Close()
	}
	commandProducerLog := kafkaconn.NewCommandLog(
		kafkaconn.NewFranzCommandLogClient(commandProducerClient, kafkaconn.FranzCommandLogClientOptions{}),
		kafkaconn.CommandLogOptions{
			CommandTopic:   runtime.CommandTopic,
			ConsumerGroup:  runtime.ConsumerGroup,
			PartitionCount: commandPartitions,
			Candidates:     index,
		},
	)
	log := core.NewSwitchableCommandLog(commandProducerLog)
	binder := func(db *sql.DB) (core.CommandEventTransactionStore, error) {
		if db == nil {
			return nil, fmt.Errorf("kafka command/event transaction requires a materialization database")
		}
		allocator := kafkaconn.NewSQLEventPositionAllocator(db, kafkaconn.SQLEventPositionAllocatorOptions{})
		transactionSession, err := kafkaconn.NewCommandWriterTransactionSession(ctx, kafkaconn.CommandWriterClientOptions{
			Runtime:         runtime,
			ClientID:        clientID + "-writer",
			TransactionalID: clientID + "-tx",
		})
		if err != nil {
			return nil, err
		}
		transactionPoolCleanup = transactionSession.CloseAllowingRebalance
		log.SetDrainLog(kafkaconn.NewCommandLog(
			kafkaconn.NewFranzCommandLogClient(transactionSession.Client(), kafkaconn.FastDrainFranzCommandLogClientOptions()),
			kafkaconn.CommandLogOptions{
				CommandTopic:   runtime.CommandTopic,
				ConsumerGroup:  runtime.ConsumerGroup,
				PartitionCount: commandPartitions,
				Candidates:     index,
			},
		))
		return core.NewBrokerCommandEventTransactionStore(
			kafkaconn.NewCommandEventTransactionClient(
				kafkaconn.NewFranzCommandEventTransactionBeginner(transactionSession, allocator),
				kafkaconn.CommandEventTransactionOptions{
					CommandTopic:  runtime.CommandTopic,
					EventTopic:    runtime.EventTopic,
					ConsumerGroup: runtime.ConsumerGroup,
				},
			),
		), nil
	}
	return core.NewIndexedCommandLog(log, index), binder, cleanup, nil
}

func openKafkaEventProjectionStore(ctx context.Context, config kafkaconn.RuntimeConfig, eventPartitions int32, clientID string, db *sql.DB) (core.EventStore, func(), error) {
	if err := validateKafkaEventProjectionBackend("kafka", config, eventPartitions); err != nil {
		return nil, func() {}, err
	}
	if db == nil {
		return nil, func() {}, fmt.Errorf("kafka event-store projection requires a materialization database")
	}
	runtime := config.Normalize()
	client, err := kafkaconn.NewEventLogRuntimeClient(ctx, kafkaconn.EventLogRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
	})
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		client.Close()
	}
	allocator := kafkaconn.NewSQLEventPositionAllocator(db, kafkaconn.SQLEventPositionAllocatorOptions{})
	store := kafkaconn.NewEventStore(
		kafkaconn.NewFranzEventLogClient(client, kafkaconn.FranzEventLogClientOptions{}),
		kafkaconn.EventLogOptions{
			EventTopic:     runtime.EventTopic,
			PartitionCount: eventPartitions,
			Partitions:     allocator,
			Head:           allocator,
		},
	)
	return store, cleanup, nil
}

func openKafkaEventShadowStore(ctx context.Context, config kafkaconn.RuntimeConfig, eventPartitions int32, clientID string) (core.EventStore, func(), error) {
	if err := validateKafkaEventShadowBackend("kafka", config, eventPartitions); err != nil {
		return nil, func() {}, err
	}
	runtime := config.Normalize()
	client, err := kafkaconn.NewEventLogRuntimeClient(ctx, kafkaconn.EventLogRuntimeClientOptions{
		Runtime:  runtime,
		ClientID: clientID,
	})
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		client.Close()
	}
	store := kafkaconn.NewEventLogShadowStore(client, kafkaconn.EventLogOptions{
		EventTopic:     runtime.EventTopic,
		PartitionCount: eventPartitions,
	}, kafkaconn.FranzEventLogClientOptions{})
	return store, cleanup, nil
}

func isSupportedCommandLogBackend(mode string) bool {
	return isSupportedLogBackend(mode) || mode == "kafka"
}

func supportedCommandLogWorkerBackends() string {
	return "memory,nats,kafka"
}

func supportedNativeCommandLogWorkerBackends() string {
	return "nats,kafka"
}

func isSupportedLogBackend(mode string) bool {
	return mode == "" || mode == "memory" || mode == "nats"
}

func isSupportedEventLogShadowBackend(mode string) bool {
	return isSupportedLogBackend(mode) || mode == "kafka"
}

func normalizeCommandLogWorkerExecutor(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "postgres", "postgresql":
		return "sql"
	case "native", "broker-native", "event-transaction":
		return "native"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedCommandLogWorkerExecutor(mode string) bool {
	return mode == "sql" || mode == "native"
}

func normalizeEventStoreProjectionMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "off", "none":
		return ""
	case "jetstream":
		return "nats"
	case "redpanda":
		return "kafka"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedEventStoreProjectionMode(mode string) bool {
	return mode == "" || mode == "nats" || mode == "kafka"
}

func validateNativeCommandEventStreams(commandWorkerMode, executorMode, commandStream, eventStream string) error {
	if commandWorkerMode != "nats" || executorMode != "native" {
		return nil
	}
	commandStream = normalizeNATSStreamName(commandStream, "BUDGIE_COMMAND_LOG")
	eventStream = normalizeNATSStreamName(eventStream, "BUDGIE_EVENT_LOG")
	if commandStream == eventStream {
		return fmt.Errorf("native NATS command-log workers require distinct command and event streams; both resolved to %q", commandStream)
	}
	return nil
}

func validateSameProcessNativeWriterProjectionStreams(roles map[string]bool, commandWorkerMode, executorMode, eventProjectionMode, writerEventStream, projectionStream string) error {
	if !roles["writer"] || commandWorkerMode != "nats" || executorMode != "native" || eventProjectionMode != "nats" {
		return nil
	}
	writerEventStream = normalizeNATSStreamName(writerEventStream, "BUDGIE_EVENT_LOG")
	projectionStream = normalizeNATSStreamName(projectionStream, "BUDGIE_EVENT_LOG")
	if writerEventStream != projectionStream {
		return fmt.Errorf("same-process native writer/projector must use the same event stream: -command-log-worker-event-nats-stream=%q -event-store-projection-nats-stream=%q", writerEventStream, projectionStream)
	}
	return nil
}

func normalizeNATSStreamName(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	return raw
}

func normalizeCommandLogWorkerOwnership(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "lease", "sql-lease":
		return "sql-lease"
	case "hash", "assignment", "hash-assignment":
		return "hash-assignment"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedCommandLogWorkerOwnership(mode string) bool {
	return mode == "sql-lease" || mode == "hash-assignment" || mode == "nats-kv"
}

func normalizeCounterStoreBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "postgres", "postgresql":
		return "sql"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedCounterStoreBackend(mode string) bool {
	return mode == "sql" || mode == "nats-kv"
}

func normalizePresenceStoreBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "postgres", "postgresql":
		return "sql"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedPresenceStoreBackend(mode string) bool {
	return mode == "sql" || mode == "nats-kv"
}

func normalizeChatStoreBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "postgres", "postgresql":
		return "sql"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedChatStoreBackend(mode string) bool {
	return mode == "sql" || mode == "nats-kv"
}

func normalizeReadCacheBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "off", "none", "disabled":
		return ""
	case "memory", "mem":
		return "memory"
	case "redis":
		return "redis"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedReadCacheBackend(mode string) bool {
	return mode == "" || mode == "memory" || mode == "redis"
}

func openReadCache(ctx context.Context, mode, redisURL, prefix string, ttl time.Duration) (core.ReadCache, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	switch normalizeReadCacheBackend(mode) {
	case "":
		return nil, func() {}, nil
	case "memory":
		return core.NewMemoryReadCache(), func() {}, nil
	case "redis":
		client, err := redisconn.NewClient(redisURL)
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			if err := client.Close(); err != nil {
				slog.Warn("close Redis read cache", "err", err)
			}
		}
		return redisconn.NewReadCache(client, redisconn.ReadCacheOptions{
			Prefix: prefix,
			TTL:    ttl,
		}), cleanup, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported read cache backend %q", mode)
	}
}

func normalizePostSearchIndexBackend(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "sql", "sql-fts", "sqlite", "postgres", "postgresql":
		return "sql-fts"
	case "meili", "meilisearch":
		return "meilisearch"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func isSupportedPostSearchIndexBackend(mode string) bool {
	return mode == "sql-fts" || mode == "meilisearch"
}

func postSearchIndexOptions(mode, meiliURL, meiliAPIKey, meiliIndex string, taskTimeout, pollInterval time.Duration) ([]core.Option, error) {
	switch mode {
	case "", "sql-fts":
		return nil, nil
	case "meilisearch":
		index, err := core.NewMeiliPostSearchIndex(core.MeiliPostSearchIndexOptions{
			Endpoint:     meiliURL,
			APIKey:       meiliAPIKey,
			Index:        meiliIndex,
			TaskTimeout:  taskTimeout,
			PollInterval: pollInterval,
		})
		if err != nil {
			return nil, err
		}
		return []core.Option{core.WithPostSearchIndex(index)}, nil
	default:
		return nil, fmt.Errorf("unsupported post search index backend %q", mode)
	}
}

func parseCommandLogWorkerGroupMembers(raw string) []string {
	seen := map[string]bool{}
	members := []string{}
	for _, part := range strings.Split(raw, ",") {
		member := strings.TrimSpace(part)
		if member == "" || seen[member] {
			continue
		}
		seen[member] = true
		members = append(members, member)
	}
	sort.Strings(members)
	return members
}

func parseCommandLogWorkerAssignmentOverrides(raw string) (map[core.LogPartition]string, error) {
	overrides := map[core.LogPartition]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		partitionSpec, ownerID, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("override %q must be kind/key=owner", part)
		}
		kind, key, ok := strings.Cut(strings.TrimSpace(partitionSpec), "/")
		if !ok {
			return nil, fmt.Errorf("override %q must use kind/key partition syntax", part)
		}
		kind = strings.TrimSpace(kind)
		key = strings.TrimSpace(key)
		ownerID = strings.TrimSpace(ownerID)
		if kind == "" || key == "" {
			return nil, fmt.Errorf("override %q has empty partition kind or key", part)
		}
		if ownerID == "" {
			return nil, fmt.Errorf("override %q has empty owner", part)
		}
		overrides[core.LogPartition{Kind: kind, Key: key}.Normalize()] = ownerID
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	return overrides, nil
}

func parseHotThreadSplits(raw string) (map[string]int, error) {
	splits := map[string]int{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		threadID, rawShards, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("split %q must use thread_id=shards", part)
		}
		threadID = strings.TrimSpace(threadID)
		rawShards = strings.TrimSpace(rawShards)
		if threadID == "" {
			return nil, fmt.Errorf("split %q has empty thread id", part)
		}
		shards, err := strconv.Atoi(rawShards)
		if err != nil || shards < 2 {
			return nil, fmt.Errorf("split %q must use shard count >= 2", part)
		}
		splits[threadID] = shards
	}
	return splits, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasLocalCommandProducerRole(roles map[string]bool, autoStats, counterCheckpoints bool) bool {
	return roles["api"] || roles["gateway"] || roles["ssh"] || roles["nntp"] || (roles["worker"] && (autoStats || counterCheckpoints))
}

func runCommandLogWorker(ctx context.Context, worker *core.CommandLogWorker, backend string, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	drain := func() {
		results, err := worker.DrainOnce(ctx)
		for _, result := range results {
			if result.Processed == 0 &&
				result.TerminalFailures == 0 &&
				result.CommitFailures == 0 &&
				result.RetryableFailure == nil &&
				!result.AssignmentLost &&
				!result.ClaimLost {
				continue
			}
			attrs := []any{
				"backend", backend,
				"partitionKind", result.Partition.Kind,
				"partitionKey", result.Partition.Key,
				"startedOffset", result.StartedOffset,
				"lastOffset", result.LastOffset,
				"processed", result.Processed,
				"applied", result.Applied,
				"terminalFailures", result.TerminalFailures,
				"commitFailures", result.CommitFailures,
				"assignmentOwnerID", result.AssignmentOwnerID,
				"assignmentGeneration", result.AssignmentGeneration,
				"claimOwnerID", result.ClaimOwnerID,
				"claimExpiresAt", result.ClaimExpiresAt,
			}
			if result.AssignmentLost {
				metrics.CommandLogAssignmentLosses.Inc()
				slog.Warn("command log writer stopped after losing partition assignment", attrs...)
				continue
			}
			if result.ClaimLost {
				slog.Warn("command log writer stopped after losing partition claim", attrs...)
				continue
			}
			if result.CommitFailure != "" {
				attrs = append(attrs, "commitFailure", result.CommitFailure)
				slog.Warn("command log writer stopped after command offset commit failure", attrs...)
				continue
			}
			if result.RetryableFailure != nil {
				attrs = append(attrs,
					"retryableCode", result.RetryableFailure.Code,
					"retryableMessage", result.RetryableFailure.Message)
				slog.Warn("command log writer stopped before retryable command", attrs...)
				continue
			}
			slog.Info("command log writer drained partition", attrs...)
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("command log writer drain failed", "backend", backend, "err", err)
			}
			return
		}
	}
	drain()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			drain()
		case <-ctx.Done():
			return
		}
	}
}

func requireEventLogPromotionReadiness(ctx context.Context, c *core.Core, candidate core.EventStore, replayLimit int, operation, source string) {
	report, err := c.CheckEventLogPromotionReadiness(ctx, candidate, replayLimit, 0)
	if err != nil {
		slog.Error("event-log promotion readiness check failed",
			"operation", operation,
			"source", source,
			"err", err,
		)
		os.Exit(1)
	}
	if !report.Ready {
		attrs := []any{
			"operation", operation,
			"source", source,
			"issues", len(report.Issues),
			"partitionsChecked", report.PartitionsChecked,
			"windowsChecked", report.WindowsChecked,
			"compared", report.Compared,
			"replayLimit", report.ReplayLimit,
		}
		if len(report.Issues) > 0 {
			first := report.Issues[0]
			attrs = append(attrs,
				"firstIssueKind", first.Kind,
				"firstIssuePartitionKind", first.Partition.Kind,
				"firstIssuePartitionKey", first.Partition.Key,
				"firstIssueMessage", first.Message,
				"firstIssueErr", first.Err,
			)
		}
		slog.Error("event-log source is not promotion-ready", attrs...)
		os.Exit(1)
	}
	slog.Info("event-log promotion readiness verified",
		"operation", operation,
		"source", source,
		"partitionsChecked", report.PartitionsChecked,
		"windowsChecked", report.WindowsChecked,
		"compared", report.Compared,
		"replayLimit", report.ReplayLimit,
	)
}

// roleAddr renders an address for the startup summary, or "disabled" when the
// role is off.
func roleAddr(enabled bool, addr any) string {
	if !enabled {
		return "disabled"
	}
	switch a := addr.(type) {
	case int:
		return fmt.Sprintf(":%d", a)
	case string:
		if a == "" {
			return "disabled"
		}
		return a
	default:
		return "enabled"
	}
}

// resolveStorage returns the effective storage backend and DSN.
// It reads the DSN from the BUDGIE_POSTGRES_DSN env var when pgDSN is empty,
// and infers postgres mode when a DSN is present but storage is still "sqlite"
// (the default), preserving backwards compatibility with -postgres-dsn usage.
func resolveStorage(storage, pgDSN string) (resolvedStorage, resolvedDSN string) {
	if pgDSN == "" {
		pgDSN = envOr("BUDGIE_POSTGRES_DSN", "")
	}
	if pgDSN != "" && storage == "sqlite" {
		storage = "postgres"
	}
	return storage, pgDSN
}

func resolveWebRoot(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	const localDist = "web/dist"
	if hasWebIndex(localDist) {
		slog.Info("serving web SPA", "path", localDist)
		return localDist
	}
	return ""
}

func hasWebIndex(root string) bool {
	if root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, "index.html"))
	return err == nil && !info.IsDir()
}

func hostKeyPath(path string) string {
	if path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "budgie_host_key"
	}
	return home + "/.ssh/budgie_host_key"
}

func ensureHostKey(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	slog.Info("generating SSH host key", "path", path)
	if err := generateHostKey(path); err != nil {
		slog.Warn("could not generate host key, SSH may fail", "err", err)
	}
}

func generateHostKey(path string) error {
	return tui.GenerateHostKey(path)
}

func obfuscateDSN(dsn string) string {
	if !strings.Contains(dsn, "@") {
		return "[redacted]"
	}
	parts := strings.SplitN(dsn, "@", 2)
	if len(parts) != 2 {
		return "[redacted]"
	}
	prefix := parts[0]
	hostPart := parts[1]
	if idx := strings.Index(prefix, "://"); idx >= 0 {
		prefix = prefix[:idx+3]
	}
	if idx := strings.Index(hostPart, "/"); idx >= 0 {
		hostPart = hostPart[idx:]
	} else {
		hostPart = "/"
	}
	return prefix + "****" + hostPart
}
