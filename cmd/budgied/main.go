// budgied is the BudgieBBS server — HTTP, WebSocket, and SSH all in one binary.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/assetstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/mailer"
	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
	"github.com/juncoflockleader/budgie-bbs/internal/nntp"
	"github.com/juncoflockleader/budgie-bbs/internal/ratelimit"
	"github.com/juncoflockleader/budgie-bbs/internal/redisconn"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
	"github.com/juncoflockleader/budgie-bbs/internal/tracing"
	"github.com/juncoflockleader/budgie-bbs/internal/tui"
	"github.com/juncoflockleader/budgie-bbs/internal/wsapi"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// buildVersion is the service version reported in traces; override at build time
// with -ldflags "-X main.buildVersion=...".
var buildVersion = "dev"

func main() {
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
		requirePolicyAccept                 = flag.Bool("require-policy-acceptance", false, "Require signup to record explicit privacy-policy acceptance (also BUDGIE_REQUIRE_POLICY_ACCEPTANCE)")
		allowSSHRegistration                = flag.Bool("allow-ssh-registration", false, "Allow guests to create an account from the SSH TUI (no captcha over SSH; off by default)")
		otelTracing                         = flag.Bool("otel-tracing", false, "Enable OpenTelemetry distributed tracing (also auto-enabled when OTEL_EXPORTER_OTLP_ENDPOINT is set); exports via OTLP/HTTP using the standard OTEL_* env vars")
		otelSampleRatio                     = flag.Float64("otel-sample-ratio", 1.0, "Head trace sampling ratio in [0,1] when tracing is enabled")
		webRoot                             = flag.String("web", "", "Path to web/dist directory for SPA serving (optional)")
		sitemapInterval                     = flag.Duration("sitemap-interval", core.DefaultSitemapInterval, "Sitemap regeneration interval / cache TTL for /sitemap.xml (also BUDGIE_SITEMAP_INTERVAL)")
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
		kafkaSecurityFlags                  = kafkaconn.RegisterRuntimeSecurityFlags(flag.CommandLine)
		writeRegionURL                      = flag.String("write-region-url", "", "Authoritative write-region HTTP base URL for regional nodes; mutating HTTP API requests are proxied there (also read from BUDGIE_WRITE_REGION_URL)")
		hotThreadSplits                     = flag.String("hot-thread-splits", "", "Comma-separated hot thread reply splits as thread_id=shards; use the same value on gateway and writer nodes")
		commandLogShadow                    = flag.String("command-log-shadow", "", "Shadow command log backend: off, memory, nats, or kafka/redpanda")
		commandLogShadowNATSStream          = flag.String("command-log-shadow-nats-stream", natsconn.DefaultJetStreamCommandLogStream, "NATS JetStream stream name for -command-log-shadow nats")
		commandLogShadowNATSReplicas        = flag.Int("command-log-shadow-nats-replicas", 1, "NATS JetStream replica count for -command-log-shadow nats")
		commandLogAuthoritative             = flag.String("command-log-authoritative", "", "Authoritative command-log submit backend: off, memory, nats, or kafka/redpanda")
		commandLogAuthoritativeNATSStream   = flag.String("command-log-authoritative-nats-stream", natsconn.DefaultJetStreamCommandLogStream, "NATS JetStream stream name for -command-log-authoritative nats")
		commandLogAuthoritativeNATSReplicas = flag.Int("command-log-authoritative-nats-replicas", 1, "NATS JetStream replica count for -command-log-authoritative nats")
		commandLogWorker                    = flag.String("command-log-worker", "", "Experimental command-log writer backend: off, memory, nats, or kafka/redpanda for SQL executor")
		commandLogWorkerNATSStream          = flag.String("command-log-worker-nats-stream", natsconn.DefaultJetStreamCommandLogStream, "NATS JetStream stream name for -command-log-worker nats")
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
		commandLogWorkerEventNATSStream     = flag.String("command-log-worker-event-nats-stream", natsconn.DefaultJetStreamEventLogStream, "NATS JetStream event-log stream name for -command-log-worker-executor native")
		commandLogWorkerEventNATSReplicas   = flag.Int("command-log-worker-event-nats-replicas", 1, "NATS JetStream event-log replica count for -command-log-worker-executor native")
		auditCommandLogMaterialization      = flag.Bool("audit-command-log-materialization", false, "Audit committed command-log offsets against SQL materialization receipts and exit; uses -command-log-worker backend/stream flags")
		checkCommandLogPromotionReadiness   = flag.Bool("check-command-log-promotion-readiness", false, "Check command-log promotion readiness and exit; requires zero uncommitted lag plus complete SQL materialization audit")
		eventLogShadow                      = flag.String("event-log-shadow", "", "Shadow event log backend for parity checks: off, memory, nats, or kafka/redpanda")
		eventLogShadowInterval              = flag.Duration("event-log-shadow-interval", 30*time.Second, "Interval for shadow event-log replay parity checks")
		eventLogShadowReplayLimit           = flag.Int("event-log-shadow-replay-limit", 100, "Maximum events per partition replay parity window")
		eventLogShadowPartitionLimit        = flag.Int("event-log-shadow-partition-limit", 100, "Maximum event partitions checked per parity interval")
		eventLogShadowStartHead             = flag.Bool("event-log-shadow-start-at-head", true, "Seed shadow parity checkpoints at current SQL heads before tail checking")
		eventLogShadowNATSStream            = flag.String("event-log-shadow-nats-stream", natsconn.DefaultJetStreamEventLogStream, "NATS JetStream stream name for -event-log-shadow nats")
		eventLogShadowNATSReplicas          = flag.Int("event-log-shadow-nats-replicas", 1, "NATS JetStream replica count for -event-log-shadow nats")
		checkEventLogPromotionReadiness     = flag.Bool("check-event-log-promotion-readiness", false, "Check SQL-vs-shadow event-log promotion readiness and exit; uses -event-log-shadow nats|kafka")
		eventStoreProjection                = flag.String("event-store-projection", "", "Experimental broker event-store projection worker backend: off, nats, or kafka/redpanda")
		eventStoreProjectionNATSStream      = flag.String("event-store-projection-nats-stream", natsconn.DefaultJetStreamEventLogStream, "NATS JetStream stream name for -event-store-projection nats")
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
		aiResponderProcessor                = flag.Bool("ai-responder-processor", true, "Run the generative-AI responder on this worker node (gated at runtime by the site-wide AI toggle and per-board config; run on a single worker)")
		aiResponderProcessorInterval        = flag.Duration("ai-responder-processor-interval", 10*time.Second, "Interval between AI responder drains")
		aiResponderProcessorBatchSize       = flag.Int("ai-responder-processor-batch-size", 5, "Maximum post events the AI responder processes per batch")
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
	commandWorkerMode := runconfig.NormalizeOptionalLogBackend(*commandLogWorker)
	commandAuthoritativeMode := runconfig.NormalizeOptionalLogBackend(*commandLogAuthoritative)
	commandLogWorkerExecutorMode := loadmodel.NormalizeCommandLogExecutor(*commandLogWorkerExecutor)
	eventStoreProjectionMode := runconfig.NormalizeOptionalLogBackend(*eventStoreProjection)
	eventStoreProjectionSourceName := strings.TrimSpace(*eventStoreProjectionSource)
	if eventStoreProjectionSourceName == "" {
		eventStoreProjectionSourceName = eventStoreProjectionMode
	}
	commandLogOneShot := *auditCommandLogMaterialization || *checkCommandLogPromotionReadiness
	commandLogWorkerOwnershipMode := runconfig.NormalizeCommandLogWorkerOwnership(*commandLogWorkerOwnership)
	counterStoreMode := runconfig.NormalizeSQLOrNATSKVBackend(*counterStoreBackend)
	presenceStoreMode := runconfig.NormalizeSQLOrNATSKVBackend(*presenceStoreBackend)
	chatStoreMode := runconfig.NormalizeSQLOrNATSKVBackend(*chatStoreBackend)
	readCacheMode := runconfig.NormalizeReadCacheBackend(*readCacheBackend)
	postSearchIndexMode := runconfig.NormalizePostSearchIndexBackend(*postSearchIndexBackend)
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
	derivedViewProcessorFlagSet := derivedViewProcessorFlags{
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
	}
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
			derivedViewProcessorFlagSet,
		)
		if err != nil {
			slog.Error("invalid derived view processor selection", "views", *derivedViewProcessors, "err", err)
			os.Exit(1)
		}
	}

	// Resolve storage backend and DSN (reads env var, handles backwards compat).
	*storage, *pgDSN = resolveStorage(*storage, *pgDSN)
	if *natsURL == "" {
		*natsURL = runconfig.EnvOr("BUDGIE_NATS_URL", "")
	}
	if *redisURL == "" {
		*redisURL = runconfig.EnvOr("BUDGIE_REDIS_URL", "")
	}
	if *kafkaBrokers == "" {
		*kafkaBrokers = runconfig.EnvOr("BUDGIE_KAFKA_BROKERS", "")
	}
	if strings.TrimSpace(*readCacheBackend) == "" {
		*readCacheBackend = runconfig.EnvOr("BUDGIE_READ_CACHE", "")
		readCacheMode = runconfig.NormalizeReadCacheBackend(*readCacheBackend)
	}
	if strings.TrimSpace(*readCachePrefix) == "" {
		*readCachePrefix = runconfig.EnvOr("BUDGIE_READ_CACHE_PREFIX", "budgie")
	}
	kafkaConfig := kafkaconn.RuntimeConfigFromOptions(*kafkaBrokers, *kafkaCommandTopic, *kafkaEventTopic, *kafkaConsumerGroup, kafkaSecurityFlags.Config())
	kafkaCommandPartitionCount := int32(*kafkaCommandPartitions)
	kafkaEventPartitionCount := int32(*kafkaEventPartitions)
	if *writeRegionURL == "" {
		*writeRegionURL = runconfig.EnvOr("BUDGIE_WRITE_REGION_URL", "")
	}
	if *meiliSearchURL == "" {
		*meiliSearchURL = runconfig.EnvOr("BUDGIE_MEILISEARCH_URL", "")
	}
	if *meiliSearchAPIKey == "" {
		*meiliSearchAPIKey = runconfig.EnvOr("BUDGIE_MEILISEARCH_API_KEY", "")
	}

	// JWT secret: flag, then env. Refuses insecure values; generates an
	// ephemeral random secret only when none is configured (dev convenience).
	secret := resolveJWTSecret(runconfig.ValueOrEnv(*jwtSecret, "BUDGIE_JWT_SECRET"))

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
		ctx, stop := runconfig.InterruptTimeoutContext(context.Background(), 0)
		defer stop()
		if err := core.MigrateSQLiteToPostgres(ctx, *dbPath, *pgDSN); err != nil {
			slog.Error("sqlite->postgres migration failed", "err", err)
			os.Exit(1)
		}
		slog.Info("sqlite->postgres migration completed", "source", *dbPath, "dsn", obfuscateDSN(*pgDSN))
		return
	}

	if *checkEventLogPromotionReadiness {
		c := openConfiguredCoreOrExit(*storage, *dbPath, *pgDSN)
		defer c.DB.Close()
		ctx, stop := runconfig.InterruptTimeoutContext(context.Background(), 0)
		defer stop()
		readinessBackend := runconfig.NormalizeEventLogPromotionReadinessBackend(*eventLogShadow)
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
		if printErr := runreport.WriteJSON(os.Stdout, report, true); printErr != nil {
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
		c := openConfiguredCoreOrExit(*storage, *dbPath, *pgDSN, backfillOptions...)
		defer c.DB.Close()
		ctx, stop := runconfig.InterruptTimeoutContext(context.Background(), 0)
		defer stop()
		source := runconfig.NormalizeProjectionRebuildSource(*rebuildSource)
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
			slog.Error("unsupported derived-view backfill source", "source", *rebuildSource, "supported", runconfig.SupportedProjectionRebuildSources())
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
		c := openConfiguredCoreOrExit(*storage, *dbPath, *pgDSN)
		ctx, stop := runconfig.InterruptTimeoutContext(context.Background(), 0)
		defer stop()
		source := runconfig.NormalizeProjectionRebuildSource(*rebuildSource)
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
			slog.Error("unsupported projection rebuild source", "source", *rebuildSource, "supported", runconfig.SupportedProjectionRebuildSources())
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
		commandMode       = runconfig.NormalizeOptionalLogBackend(*commandLogShadow)
		shadowMode        = runconfig.NormalizeOptionalLogBackend(*eventLogShadow)
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
	if !runconfig.IsSupportedOptionalBrokerLogBackend(commandMode) {
		slog.Error("unsupported command log shadow backend", "backend", *commandLogShadow, "supported", runconfig.SupportedOptionalBrokerLogBackends())
		os.Exit(1)
	}
	if commandAuthoritativeMode == "kafka" {
		if err := validateKafkaCommandLogBackend(commandAuthoritativeMode, kafkaConfig, kafkaCommandPartitionCount); err != nil {
			slog.Error("invalid authoritative command log Kafka config", "backend", *commandLogAuthoritative, "err", err)
			os.Exit(1)
		}
	}
	if !runconfig.IsSupportedOptionalBrokerLogBackend(commandAuthoritativeMode) {
		slog.Error("unsupported authoritative command log backend", "backend", *commandLogAuthoritative, "supported", runconfig.SupportedOptionalBrokerLogBackends())
		os.Exit(1)
	}
	if commandWorkerMode == "kafka" {
		if err := validateKafkaCommandWorkerBackend(commandWorkerMode, commandLogWorkerExecutorMode, kafkaConfig, kafkaCommandPartitionCount, kafkaEventPartitionCount); err != nil {
			slog.Error("invalid command log worker Kafka config", "backend", *commandLogWorker, "executor", commandLogWorkerExecutorMode, "err", err)
			os.Exit(1)
		}
	}
	if !runconfig.IsSupportedOptionalBrokerLogBackend(commandWorkerMode) {
		slog.Error("unsupported command log worker backend", "backend", *commandLogWorker, "supported", runconfig.SupportedOptionalBrokerLogBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedCommandLogWorkerOwnership(commandLogWorkerOwnershipMode) {
		slog.Error("unsupported command log worker ownership", "ownership", *commandLogWorkerOwnership, "supported", runconfig.SupportedCommandLogWorkerOwnershipModes())
		os.Exit(1)
	}
	if !loadmodel.IsSupportedCommandLogDrainExecutorMode(commandLogWorkerExecutorMode) {
		slog.Error("unsupported command log worker executor", "executor", *commandLogWorkerExecutor, "supported", runconfig.SupportedCommandLogExecutors())
		os.Exit(1)
	}
	if shadowMode == "kafka" {
		if err := validateKafkaEventShadowBackend(shadowMode, kafkaConfig, kafkaEventPartitionCount); err != nil {
			slog.Error("invalid event log shadow Kafka config", "backend", *eventLogShadow, "err", err)
			os.Exit(1)
		}
	}
	if !runconfig.IsSupportedOptionalBrokerLogBackend(shadowMode) {
		slog.Error("unsupported event log shadow backend", "backend", *eventLogShadow, "supported", runconfig.SupportedOptionalBrokerLogBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedOptionalNATSKafkaBackend(eventStoreProjectionMode) {
		slog.Error("unsupported event-store projection backend", "backend", *eventStoreProjection, "supported", runconfig.SupportedOptionalNATSKafkaBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedSQLOrNATSKVBackend(counterStoreMode) {
		slog.Error("unsupported counter store backend", "backend", *counterStoreBackend, "supported", runconfig.SupportedSQLOrNATSKVBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedSQLOrNATSKVBackend(presenceStoreMode) {
		slog.Error("unsupported presence store backend", "backend", *presenceStoreBackend, "supported", runconfig.SupportedSQLOrNATSKVBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedSQLOrNATSKVBackend(chatStoreMode) {
		slog.Error("unsupported chat store backend", "backend", *chatStoreBackend, "supported", runconfig.SupportedSQLOrNATSKVBackends())
		os.Exit(1)
	}
	if !runconfig.IsSupportedReadCacheBackend(readCacheMode) {
		slog.Error("unsupported read cache backend", "backend", *readCacheBackend, "supported", runconfig.SupportedReadCacheBackends())
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
	if !runconfig.IsSupportedPostSearchIndexBackend(postSearchIndexMode) {
		slog.Error("unsupported post search index backend", "backend", *postSearchIndexBackend, "supported", runconfig.SupportedPostSearchIndexBackends())
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
	if !roles["worker"] {
		if message := missingDerivedViewProcessorWorkerRole(derivedViewProcessorFlagSet); message != "" {
			slog.Error(message, "roles", *roleList)
			os.Exit(1)
		}
	}
	if conflict, ok := derivedViewWatermarkOwnershipConflict(derivedViewWatermarkViews, derivedViewProcessorFlagSet, postSearchIndexMode); ok {
		slog.Error(conflict.message, "views", *derivedViewWatermarks, "hint", conflict.hint)
		os.Exit(1)
	}
	if commandLogOneShot && commandWorkerMode == "" {
		slog.Error("command-log one-shot check requires a command-log backend", "flag", "-command-log-worker", "supported", runconfig.SupportedBrokerLogBackends())
		os.Exit(1)
	}
	if roles["writer"] && commandWorkerMode == "" {
		slog.Error("writer role requires a command-log worker backend", "flag", "-command-log-worker", "supported", runconfig.SupportedBrokerLogBackends())
		os.Exit(1)
	}
	if commandLogWorkerExecutorMode == "native" && commandWorkerMode == "" {
		slog.Error("native command-log worker executor requires a command-log worker backend", "flag", "-command-log-worker", "supported", runconfig.SupportedNATSKafkaBackends())
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
		if !slices.Contains(commandLogWorkerGroupMemberIDs, *commandLogWorkerID) {
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
			if !slices.Contains(commandLogWorkerGroupMemberIDs, ownerID) {
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
		if !slices.Contains(commandLogWorkerGroupMemberIDs, *commandLogWorkerID) {
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
		slog.Error("unsupported command log shadow backend", "backend", *commandLogShadow, "supported", runconfig.SupportedOptionalBrokerLogBackends())
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
		slog.Error("unsupported authoritative command log backend", "backend", *commandLogAuthoritative, "supported", runconfig.SupportedOptionalBrokerLogBackends())
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
		slog.Error("unsupported command log worker backend", "backend", *commandLogWorker, "supported", runconfig.SupportedOptionalBrokerLogBackends())
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
		slog.Error("unsupported event log shadow backend", "backend", *eventLogShadow, "supported", runconfig.SupportedOptionalBrokerLogBackends())
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
	default:
		slog.Info("starting budgied", "storage", "sqlite", "db", *dbPath)
	}
	c = openConfiguredCoreOrExit(*storage, *dbPath, *pgDSN, coreOptions...)
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

	ctx, stop := runconfig.InterruptTimeoutContext(context.Background(), 0)
	defer stop()

	if *checkCommandLogPromotionReadiness {
		if commandLog == nil {
			slog.Error("command-log promotion readiness check has no command log", "flag", "-command-log-worker")
			os.Exit(1)
		}
		report, err := c.CheckCommandLogPromotionReadiness(ctx, commandLog, loadmodel.CommandLogPromotionReadinessConfig{
			PartitionLimit: *commandLogWorkerLimit,
			BatchSize:      *commandLogWorkerBatchSize,
		})
		if printErr := runreport.WriteJSON(os.Stdout, report, true); printErr != nil {
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
		report, err := c.AuditCommandLogMaterialization(ctx, commandLog, loadmodel.CommandLogMaterializationAuditConfig{
			PartitionLimit: *commandLogWorkerLimit,
			BatchSize:      *commandLogWorkerBatchSize,
		})
		if printErr := runreport.WriteJSON(os.Stdout, report, true); printErr != nil {
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
	capSecret := runconfig.ValueOrEnv(*captchaSecret, "BUDGIE_CAPTCHA_SECRET")
	if capSecret == "" {
		capSecret = string(secret)
	}
	c.SetCaptcha(core.CaptchaConfig{
		Mode:      runconfig.ValueOrEnv(*captchaMode, "BUDGIE_CAPTCHA_MODE"),
		Provider:  runconfig.ValueOrEnv(*captchaProvider, "BUDGIE_CAPTCHA_PROVIDER"),
		SiteKey:   runconfig.ValueOrEnv(*captchaSiteKey, "BUDGIE_CAPTCHA_SITE_KEY"),
		Secret:    capSecret,
		VerifyURL: *captchaVerifyURL,
	})

	// Outbound email + verification (off unless a From address is configured).
	// Default transport is direct-to-MX; an SMTP relay (BUDGIE_SMTP_HOST) takes
	// over when set, which is also how an ESP is wired in.
	mailModeResolved := runconfig.ValueOrEnv(*mailMode, "BUDGIE_MAIL_MODE")
	mailFromResolved := runconfig.ValueOrEnv(*mailFromAddr, "BUDGIE_MAIL_FROM")
	smtpHostResolved := runconfig.ValueOrEnv(*smtpHost, "BUDGIE_SMTP_HOST")
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
		Username: runconfig.ValueOrEnv(*smtpUser, "BUDGIE_SMTP_USER"),
		Password: runconfig.ValueOrEnv(*smtpPassword, "BUDGIE_SMTP_PASSWORD"),
	})
	if err != nil {
		slog.Error("mailer configuration invalid", "err", err)
		os.Exit(1)
	}
	c.SetMailer(mailerInstance, mailFromResolved, *requireEmailVerify, runconfig.ValueOrEnv(*publicURL, "BUDGIE_PUBLIC_URL"))
	// Classify the backend so verification defaults make sense per environment:
	//   no mailer        -> verification stays off (nothing can be sent)
	//   loopback relay   -> mailpit-style dev catcher; surface its web inbox
	//   remote relay/MX  -> real provider; verification enforced by default
	if mailerInstance == nil {
		if *requireEmailVerify {
			slog.Info("email verification requested but no mailer configured; verification disabled (set -mail-from to enable)")
		}
	} else {
		devInbox := runconfig.ValueOrEnv(*mailInboxURL, "BUDGIE_MAIL_INBOX_URL")
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

	// Privacy policy acceptance gate (off unless required).
	requirePolicy := *requirePolicyAccept || strings.EqualFold(os.Getenv("BUDGIE_REQUIRE_POLICY_ACCEPTANCE"), "true")
	c.SetPrivacyPolicy(requirePolicy)
	if requirePolicy {
		slog.Info("signup requires privacy policy acceptance")
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
	if label, err := startDerivedViewProcessors(ctx, c, []derivedViewProcessorStarter{
		{
			label:      "post search",
			enabled:    postSearchProcessor,
			interval:   postSearchProcessorInterval,
			batchSize:  postSearchProcessorBatchSize,
			start:      derivedViewCoreStarter((*core.Core).StartPostSearchProcessor),
			extraAttrs: func() []any { return []any{"asyncPostSearch", *asyncPostSearch} },
		},
		{label: "digest search", enabled: digestSearchProcessor, interval: digestSearchProcessorInterval, batchSize: digestSearchProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartDigestSearchProcessor)},
		{label: "community stats", enabled: communityStatsProcessor, interval: communityStatsProcessorInterval, batchSize: communityStatsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartCommunityStatsProcessor)},
		{label: "latest feed", enabled: latestFeedProcessor, interval: latestFeedProcessorInterval, batchSize: latestFeedProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartLatestFeedProcessor)},
		{label: "resident feed", enabled: residentFeedProcessor, interval: residentFeedProcessorInterval, batchSize: residentFeedProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartResidentFeedProcessor)},
		{label: "board summaries", enabled: boardSummariesProcessor, interval: boardSummariesProcessorInterval, batchSize: boardSummariesProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartBoardSummariesProcessor)},
		{label: "unread thread summaries", enabled: unreadThreadSummariesProcessor, interval: unreadThreadSummariesInterval, batchSize: unreadThreadSummariesBatchSize, start: derivedViewCoreStarter((*core.Core).StartUnreadThreadSummariesProcessor)},
		{label: "board rankings", enabled: boardRankingsProcessor, interval: boardRankingsProcessorInterval, batchSize: boardRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartBoardRankingsProcessor)},
		{label: "thread rankings", enabled: threadRankingsProcessor, interval: threadRankingsProcessorInterval, batchSize: threadRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartThreadRankingsProcessor)},
		{label: "reply rankings", enabled: replyRankingsProcessor, interval: replyRankingsProcessorInterval, batchSize: replyRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartReplyRankingsProcessor)},
		{label: "user rankings", enabled: userRankingsProcessor, interval: userRankingsProcessorInterval, batchSize: userRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartUserRankingsProcessor)},
	}); err != nil {
		slog.Error(label+" processor failed to start", "err", err)
		os.Exit(1)
	}
	if roles["worker"] && *aiResponderProcessor {
		if _, err := c.StartAIResponderProcessor(ctx, *aiResponderProcessorInterval, *aiResponderProcessorBatchSize); err != nil {
			slog.Error("ai responder processor failed to start", "err", err)
			os.Exit(1)
		}
		slog.Info("ai responder processor enabled",
			"interval", aiResponderProcessorInterval.String(),
			"batchSize", *aiResponderProcessorBatchSize)
	}
	if label, err := startDerivedViewProcessors(ctx, c, []derivedViewProcessorStarter{
		{label: "blessing rankings", enabled: blessingRankingsProcessor, interval: blessingRankingsProcessorInterval, batchSize: blessingRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartBlessingRankingsProcessor)},
		{label: "archive rankings", enabled: archiveRankingsProcessor, interval: archiveRankingsProcessorInterval, batchSize: archiveRankingsProcessorBatchSize, start: derivedViewCoreStarter((*core.Core).StartArchiveRankingsProcessor)},
	}); err != nil {
		slog.Error(label+" processor failed to start", "err", err)
		os.Exit(1)
	}

	// OpenTelemetry distributed tracing (opt-in). Enabled by -otel-tracing or by
	// presence of an OTLP endpoint env var. Installs a global tracer provider +
	// W3C propagator so spans flow across nodes; the HTTP server and write-region
	// proxy are instrumented below.
	tracingEnabled := *otelTracing ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
	hostname, _ := os.Hostname()
	tracingShutdown, err := tracing.Init(ctx, tracing.Config{
		Enabled:     tracingEnabled,
		ServiceName: "budgied",
		Version:     buildVersion,
		NodeID:      hostname,
		SampleRatio: *otelSampleRatio,
	})
	if err != nil {
		slog.Warn("tracing disabled: init failed", "err", err)
		tracingEnabled = false
	} else if tracingEnabled {
		slog.Info("OpenTelemetry tracing enabled", "sampleRatio", *otelSampleRatio)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracingShutdown(shutdownCtx)
	}()

	// Cluster-wide brute-force rate limiting: when Redis is configured, back the
	// credential limiters with a shared store so login/2FA/recovery budgets are
	// enforced across all nodes (not just per-process). Falls back to per-node
	// limiting if Redis is unavailable at request time.
	var rateLimitStore ratelimit.Store
	if strings.TrimSpace(*redisURL) != "" {
		rlClient, err := redisconn.NewClient(*redisURL)
		if err != nil {
			slog.Error("rate-limit redis client", "flag", "-redis", "err", err)
			os.Exit(1)
		}
		defer func() { _ = rlClient.Close() }()
		rateLimitStore = ratelimit.NewRedisStore(rlClient, *readCachePrefix)
		slog.Info("cluster-wide rate limiting enabled", "backend", "redis", "prefix", *readCachePrefix)
	}

	// Site assets (logo/banner): use an external object store + CDN when
	// configured (BUDGIE_ASSET_S3_BUCKET…), else keep bytes in the DB.
	if err := configureAssetStore(c); err != nil {
		slog.Error("site asset store init failed", "err", err)
		os.Exit(1)
	}

	// HTTP listener — always started so /healthz, /readyz, /metrics are reachable
	// on every node. The full API/WS/SPA is mounted only on the api role; the
	// gateway role gets live WS/SSE/poll transports without the REST API surface.
	httpSrv := httpapi.New(c, secret)
	httpSrv.EnableClusterRateLimiting(rateLimitStore)
	if err := httpSrv.SetWriteRegionURL(*writeRegionURL); err != nil {
		slog.Error("invalid write region URL", "flag", "-write-region-url", "value", *writeRegionURL, "err", err)
		os.Exit(1)
	}
	if root := resolveWebRoot(*webRoot); root != "" {
		httpSrv.SetWebRoot(root)
	}
	// Search-engine support: /robots.txt + /sitemap.xml. The public base URL is
	// shared with email links; the regeneration interval (cache TTL) is settable
	// via -sitemap-interval or BUDGIE_SITEMAP_INTERVAL.
	sitemapIv := *sitemapInterval
	if env := strings.TrimSpace(os.Getenv("BUDGIE_SITEMAP_INTERVAL")); env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			sitemapIv = d
		} else {
			slog.Warn("invalid BUDGIE_SITEMAP_INTERVAL; using flag/default", "value", env, "err", err)
		}
	}
	httpSrv.SetSEOConfig(runconfig.ValueOrEnv(*publicURL, "BUDGIE_PUBLIC_URL"), sitemapIv)
	mux := http.NewServeMux()
	writeRegionProxyEnabled := strings.TrimSpace(*writeRegionURL) != ""
	wsAllowCommands := !writeRegionProxyEnabled || commandAuthoritativeMode != ""
	// Propagate trace context across the write-region proxy hop so a request
	// traced on this node continues as one trace on the write region.
	if tracingEnabled && writeRegionProxyEnabled {
		httpSrv.SetWriteRegionTransport(otelhttp.NewTransport(http.DefaultTransport))
	}
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
	// ReadHeaderTimeout + MaxHeaderBytes guard against Slowloris and oversized
	// header floods. ReadTimeout/WriteTimeout are intentionally omitted: the WS
	// and SSE endpoints are long-lived and a global write deadline would sever
	// them. Per-endpoint body-size caps are applied in the httpapi layer.
	// Instrument the HTTP surface with otelhttp when tracing is enabled: every
	// request becomes a server span (continuing any incoming traceparent), and
	// the span context flows into handlers via the request context.
	var rootHandler http.Handler = mux
	if tracingEnabled {
		rootHandler = otelhttp.NewHandler(mux, "budgied.http")
	}
	srv := &http.Server{
		Addr:              *httpAddr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
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
		tuiSrv.EnableClusterRateLimiting(rateLimitStore)
		tuiSrv.SetAllowRegistration(*allowSSHRegistration)
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

// configureAssetStore wires an external S3-compatible object store for site
// assets when BUDGIE_ASSET_S3_BUCKET is set; otherwise assets stay in the DB.
//
//	BUDGIE_ASSET_S3_BUCKET, _REGION (default "auto"), _ENDPOINT (R2/MinIO),
//	_ACCESS_KEY, _SECRET_KEY; BUDGIE_ASSET_BASE_URL = public/CDN base URL.
func configureAssetStore(c *core.Core) error {
	bucket := strings.TrimSpace(os.Getenv("BUDGIE_ASSET_S3_BUCKET"))
	if bucket == "" {
		return nil
	}
	store, err := assetstore.NewS3(assetstore.S3Config{
		Bucket:        bucket,
		Region:        os.Getenv("BUDGIE_ASSET_S3_REGION"),
		Endpoint:      os.Getenv("BUDGIE_ASSET_S3_ENDPOINT"),
		AccessKey:     os.Getenv("BUDGIE_ASSET_S3_ACCESS_KEY"),
		SecretKey:     os.Getenv("BUDGIE_ASSET_S3_SECRET_KEY"),
		PublicBaseURL: os.Getenv("BUDGIE_ASSET_BASE_URL"),
	})
	if err != nil {
		return err
	}
	c.SetAssetStore(store)
	slog.Info("site assets: external object store enabled", "bucket", bucket, "publicBase", store.PublicBaseURL())
	return nil
}

// minJWTSecretLen is the minimum accepted length for a configured JWT/HMAC
// signing secret. The token signing is HS256, so the secret is the entire
// security of every session — short or guessable secrets allow token forgery.
const minJWTSecretLen = 32

// resolveJWTSecret turns the configured secret (flag or env) into the signing
// key, refusing insecure values. An empty secret yields a random ephemeral key
// (fine for single-node dev; sessions just don't survive a restart). A
// configured secret must be at least minJWTSecretLen bytes and must not be the
// historical placeholder, otherwise the process refuses to start — a known or
// short HMAC key lets anyone forge a token for any account, including admins.
func resolveJWTSecret(configured string) []byte {
	if configured == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			slog.Error("could not generate an ephemeral JWT secret", "err", err)
			os.Exit(1)
		}
		slog.Warn("no JWT secret configured; generated a random ephemeral secret — " +
			"sessions will not survive a restart and will not match across nodes. " +
			"Set BUDGIE_JWT_SECRET (>= 32 chars) for production.")
		return []byte(hex.EncodeToString(buf))
	}
	if err := validateConfiguredJWTSecret(configured); err != nil {
		slog.Error("refusing to start with an insecure JWT secret", "err", err,
			"hint", "set BUDGIE_JWT_SECRET to a unique random value (>= 32 chars)")
		os.Exit(1)
	}
	return []byte(configured)
}

// validateConfiguredJWTSecret rejects a configured secret that is the historical
// placeholder or too short to resist offline guessing/forgery. Returns nil when
// the secret is acceptable. Pure (no exit/log) so it is unit-testable.
func validateConfiguredJWTSecret(s string) error {
	if s == "change-me-in-production" {
		return fmt.Errorf("the placeholder secret 'change-me-in-production' must not be used")
	}
	if len(s) < minJWTSecretLen {
		return fmt.Errorf("secret is %d bytes; need at least %d", len(s), minJWTSecretLen)
	}
	return nil
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

type derivedViewDedicatedProcessor struct {
	view     string
	label    string
	flagName string
	enabled  *bool
}

type derivedViewProcessorStarter struct {
	label      string
	enabled    *bool
	interval   *time.Duration
	batchSize  *int
	start      func(context.Context, *core.Core, time.Duration, int) error
	extraAttrs func() []any
}

type derivedViewWatermarkConflict struct {
	message string
	hint    string
}

func derivedViewDedicatedProcessors(flags derivedViewProcessorFlags) []derivedViewDedicatedProcessor {
	return []derivedViewDedicatedProcessor{
		{view: core.DerivedViewPostSearch, label: "post search", flagName: "post-search-processor", enabled: flags.postSearchProcessor},
		{view: core.DerivedViewDigestSearch, label: "digest search", flagName: "digest-search-processor", enabled: flags.digestSearchProcessor},
		{view: core.DerivedViewCommunityStats, label: "community stats", flagName: "community-stats-processor", enabled: flags.communityStatsProcessor},
		{view: core.DerivedViewLatestFeed, label: "latest feed", flagName: "latest-feed-processor", enabled: flags.latestFeedProcessor},
		{view: core.DerivedViewResidentFeed, label: "resident feed", flagName: "resident-feed-processor", enabled: flags.residentFeedProcessor},
		{view: core.DerivedViewBoardSummaries, label: "board summaries", flagName: "board-summaries-processor", enabled: flags.boardSummariesProcessor},
		{view: core.DerivedViewUnreadThreads, label: "unread thread summaries", flagName: "unread-thread-summaries-processor", enabled: flags.unreadThreadSummariesProcessor},
		{view: core.DerivedViewBoardRankings, label: "board rankings", flagName: "board-rankings-processor", enabled: flags.boardRankingsProcessor},
		{view: core.DerivedViewThreadRankings, label: "thread rankings", flagName: "thread-rankings-processor", enabled: flags.threadRankingsProcessor},
		{view: core.DerivedViewReplyRankings, label: "reply rankings", flagName: "reply-rankings-processor", enabled: flags.replyRankingsProcessor},
		{view: core.DerivedViewUserRankings, label: "user rankings", flagName: "user-rankings-processor", enabled: flags.userRankingsProcessor},
		{view: core.DerivedViewBlessingRankings, label: "blessing rankings", flagName: "blessing-rankings-processor", enabled: flags.blessingRankingsProcessor},
		{view: core.DerivedViewArchiveRankings, label: "archive rankings", flagName: "archive-rankings-processor", enabled: flags.archiveRankingsProcessor},
	}
}

func startDerivedViewProcessors(ctx context.Context, c *core.Core, processors []derivedViewProcessorStarter) (string, error) {
	for _, processor := range processors {
		if !boolValue(processor.enabled) {
			continue
		}
		interval := durationValue(processor.interval)
		batchSize := intValue(processor.batchSize)
		if err := processor.start(ctx, c, interval, batchSize); err != nil {
			return processor.label, err
		}
		attrs := []any{}
		if processor.extraAttrs != nil {
			attrs = append(attrs, processor.extraAttrs()...)
		}
		attrs = append(attrs, "interval", interval.String(), "batchSize", batchSize)
		slog.Info(processor.label+" processor enabled", attrs...)
	}
	return "", nil
}

func derivedViewCoreStarter[T any](start func(*core.Core, context.Context, time.Duration, int) (T, error)) func(context.Context, *core.Core, time.Duration, int) error {
	return func(ctx context.Context, c *core.Core, interval time.Duration, batchSize int) error {
		_, err := start(c, ctx, interval, batchSize)
		return err
	}
}

func applyDerivedViewProcessorSelection(raw string, flags derivedViewProcessorFlags) ([]string, error) {
	resolved, err := core.ResolveDerivedViews([]string{raw})
	if err != nil {
		return nil, err
	}
	processors := derivedViewDedicatedProcessors(flags)
	for _, view := range resolved {
		switch view {
		case core.DerivedViewPostSearch:
			setBool(flags.asyncPostSearch, true)
		case core.DerivedViewCommunityStatHistory:
			setBool(flags.asyncCommunityStatHistory, true)
		}
		for _, processor := range processors {
			if processor.view == view {
				setBool(processor.enabled, true)
				break
			}
		}
	}
	return resolved, nil
}

func missingDerivedViewProcessorWorkerRole(flags derivedViewProcessorFlags) string {
	for _, processor := range derivedViewDedicatedProcessors(flags) {
		if boolValue(processor.enabled) {
			return processor.label + " processor requires the worker role"
		}
	}
	return ""
}

func derivedViewWatermarkOwnershipConflict(views []string, flags derivedViewProcessorFlags, postSearchIndexMode string) (derivedViewWatermarkConflict, bool) {
	postSearchOwned := boolValue(flags.asyncPostSearch) || boolValue(flags.postSearchProcessor) || postSearchIndexMode != "sql-fts"
	if postSearchOwned && slices.Contains(views, core.DerivedViewPostSearch) {
		return derivedViewProcessorWatermarkConflict("post search", core.DerivedViewPostSearch, "post-search-processor"), true
	}
	for _, processor := range derivedViewDedicatedProcessors(flags) {
		if processor.view == core.DerivedViewPostSearch {
			continue
		}
		if boolValue(processor.enabled) && slices.Contains(views, processor.view) {
			return derivedViewProcessorWatermarkConflict(processor.label, processor.view, processor.flagName), true
		}
	}
	if boolValue(flags.asyncCommunityStatHistory) && slices.Contains(views, core.DerivedViewCommunityStatHistory) {
		return derivedViewWatermarkConflict{
			message: "community stat history ownership cannot use compatibility watermark sync for community_stat_history",
			hint:    "remove community_stat_history from -derived-view-watermarks; queued snapshot jobs advance the view watermark",
		}, true
	}
	return derivedViewWatermarkConflict{}, false
}

func derivedViewProcessorWatermarkConflict(label, view, flagName string) derivedViewWatermarkConflict {
	return derivedViewWatermarkConflict{
		message: fmt.Sprintf("%s processor ownership cannot use compatibility watermark sync for %s", label, view),
		hint:    fmt.Sprintf("remove %s from -derived-view-watermarks and run -%s on a worker node", view, flagName),
	}
}

func setBool(target *bool, value bool) {
	if target != nil {
		*target = value
	}
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func durationValue(value *time.Duration) time.Duration {
	if value == nil {
		return 0
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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

func validateKafkaCommandLogBackend(mode string, config kafkaconn.RuntimeConfig, partitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateCommandLogRuntime(partitions); err != nil {
		return fmt.Errorf("kafka command-log backend requires %w", err)
	}
	return nil
}

func validateKafkaEventShadowBackend(mode string, config kafkaconn.RuntimeConfig, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateEventLogRuntime(eventPartitions); err != nil {
		return fmt.Errorf("kafka event-log shadow requires %w", err)
	}
	return nil
}

func validateKafkaCommandWorkerBackend(mode, executorMode string, config kafkaconn.RuntimeConfig, commandPartitions, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if executorMode == "native" {
		if err := config.ValidateCommandEventRuntime(commandPartitions, eventPartitions); err != nil {
			return fmt.Errorf("native Kafka command-log worker requires %w", err)
		}
		return nil
	}
	return validateKafkaCommandLogBackend(mode, config, commandPartitions)
}

func validateKafkaEventProjectionBackend(mode string, config kafkaconn.RuntimeConfig, eventPartitions int32) error {
	if mode != "kafka" {
		return nil
	}
	if err := config.ValidateEventLogRuntime(eventPartitions); err != nil {
		return fmt.Errorf("kafka event-store projection requires %w", err)
	}
	return nil
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
	source := runconfig.NormalizeProjectionRebuildSource(options.Source)
	switch source {
	case "nats":
		if strings.TrimSpace(options.NATSURL) == "" {
			return "", nil, func() {}, fmt.Errorf("NATS projection rebuild source requires a NATS URL (-nats or BUDGIE_NATS_URL)")
		}
		store, cleanup, err := natsconn.OpenJetStreamEventStore(ctx, options.NATSURL, natsconn.JetStreamEventLogOptions{
			Stream:   options.NATSStream,
			Replicas: options.NATSReplicas,
			ReadOnly: true,
		})
		if err != nil {
			return "", nil, func() {}, fmt.Errorf("nats projection rebuild source init failed: %w", err)
		}
		return source, store, cleanup, nil
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
		return "", nil, func() {}, fmt.Errorf("unsupported projection rebuild source %q (supported: %s)", options.Source, runconfig.SupportedProjectionRebuildSources())
	}
}

func openEventLogPromotionReadinessStore(ctx context.Context, mode, natsURL, natsStream string, natsReplicas int, kafkaConfig kafkaconn.RuntimeConfig, kafkaEventPartitions int32, clientID string, db *sql.DB) (core.EventStore, func(), error) {
	mode = runconfig.NormalizeEventLogPromotionReadinessBackend(mode)
	switch mode {
	case "nats":
		if strings.TrimSpace(natsURL) == "" {
			return nil, func() {}, fmt.Errorf("event-log promotion readiness requires a NATS URL (-nats or BUDGIE_NATS_URL)")
		}
		store, cleanup, err := natsconn.OpenJetStreamEventStore(ctx, natsURL, natsconn.JetStreamEventLogOptions{
			Stream:   natsStream,
			Replicas: natsReplicas,
			ReadOnly: true,
		})
		if err != nil {
			return nil, func() {}, fmt.Errorf("nats event-log promotion source init failed: %w", err)
		}
		return store, cleanup, nil
	case "kafka":
		return openKafkaEventProjectionStore(ctx, kafkaConfig, kafkaEventPartitions, clientID, db)
	default:
		return nil, func() {}, fmt.Errorf("unsupported event-log promotion readiness backend %q (supported: %s)", mode, runconfig.SupportedEventLogPromotionReadinessBackends())
	}
}

func openKafkaCommandLog(ctx context.Context, config kafkaconn.RuntimeConfig, partitions int32, clientID string, index core.CommandLogPartitionIndexer) (core.CommandLog, func(), error) {
	if err := validateKafkaCommandLogBackend("kafka", config, partitions); err != nil {
		return nil, func() {}, err
	}
	if index == nil {
		return nil, func() {}, fmt.Errorf("kafka command log requires a command partition index")
	}
	log, cleanup, err := kafkaconn.OpenRuntimeCommandLog(ctx, config, clientID, kafkaconn.CommandLogOptions{
		PartitionCount: partitions,
		Candidates:     index,
	}, kafkaconn.FranzCommandLogClientOptions{})
	if err != nil {
		return nil, func() {}, err
	}
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
	commandProducerLog, commandProducerCleanup, err := kafkaconn.OpenRuntimeCommandProducerLog(ctx, config, clientID+"-command-producer", kafkaconn.CommandLogOptions{
		PartitionCount: commandPartitions,
		Candidates:     index,
	}, kafkaconn.FranzCommandLogClientOptions{})
	if err != nil {
		return nil, nil, func() {}, err
	}
	var transactionPoolCleanup func()
	cleanup := func() {
		if transactionPoolCleanup != nil {
			transactionPoolCleanup()
		}
		commandProducerCleanup()
	}
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
	return kafkaconn.OpenSQLPositionedRuntimeEventStore(ctx, config, clientID, db, kafkaconn.SQLEventPositionedEventStoreOptions{
		PartitionCount: eventPartitions,
	}, kafkaconn.FranzEventLogClientOptions{})
}

func openKafkaEventShadowStore(ctx context.Context, config kafkaconn.RuntimeConfig, eventPartitions int32, clientID string) (core.EventStore, func(), error) {
	if err := validateKafkaEventShadowBackend("kafka", config, eventPartitions); err != nil {
		return nil, func() {}, err
	}
	return kafkaconn.OpenRuntimeEventShadowStore(ctx, config, clientID, kafkaconn.EventLogOptions{
		PartitionCount: eventPartitions,
	}, kafkaconn.FranzEventLogClientOptions{})
}

func validateNativeCommandEventStreams(commandWorkerMode, executorMode, commandStream, eventStream string) error {
	if commandWorkerMode != "nats" || executorMode != "native" {
		return nil
	}
	commandStream = natsconn.JetStreamName(commandStream, natsconn.DefaultJetStreamCommandLogStream)
	eventStream = natsconn.JetStreamName(eventStream, natsconn.DefaultJetStreamEventLogStream)
	if commandStream == eventStream {
		return fmt.Errorf("native NATS command-log workers require distinct command and event streams; both resolved to %q", commandStream)
	}
	return nil
}

func validateSameProcessNativeWriterProjectionStreams(roles map[string]bool, commandWorkerMode, executorMode, eventProjectionMode, writerEventStream, projectionStream string) error {
	if !roles["writer"] || commandWorkerMode != "nats" || executorMode != "native" || eventProjectionMode != "nats" {
		return nil
	}
	writerEventStream = natsconn.JetStreamName(writerEventStream, natsconn.DefaultJetStreamEventLogStream)
	projectionStream = natsconn.JetStreamName(projectionStream, natsconn.DefaultJetStreamEventLogStream)
	if writerEventStream != projectionStream {
		return fmt.Errorf("same-process native writer/projector must use the same event stream: -command-log-worker-event-nats-stream=%q -event-store-projection-nats-stream=%q", writerEventStream, projectionStream)
	}
	return nil
}

func openReadCache(ctx context.Context, mode, redisURL, prefix string, ttl time.Duration) (core.ReadCache, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	switch runconfig.NormalizeReadCacheBackend(mode) {
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

func postSearchIndexOptions(mode, meiliURL, meiliAPIKey, meiliIndex string, taskTimeout, pollInterval time.Duration) ([]core.Option, error) {
	switch runconfig.NormalizePostSearchIndexBackend(mode) {
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

func hasLocalCommandProducerRole(roles map[string]bool, autoStats, counterCheckpoints bool) bool {
	return roles["api"] || roles["gateway"] || roles["ssh"] || roles["nntp"] || (roles["worker"] && (autoStats || counterCheckpoints))
}

func openConfiguredCoreOrExit(storage, dbPath, pgDSN string, options ...core.Option) *core.Core {
	c, err := openConfiguredCore(storage, dbPath, pgDSN, options...)
	if err == nil {
		return c
	}
	if storage == "postgres" && pgDSN == "" {
		slog.Error("postgres DSN required", "flag", "-postgres-dsn", "env", "BUDGIE_POSTGRES_DSN")
	} else {
		slog.Error("core init failed", "err", err)
	}
	os.Exit(1)
	return nil
}

func openConfiguredCore(storage, dbPath, pgDSN string, options ...core.Option) (*core.Core, error) {
	if storage == "postgres" {
		if pgDSN == "" {
			return nil, fmt.Errorf("postgres DSN required")
		}
		return core.NewPostgres(pgDSN, options...)
	}
	return core.New(dbPath, options...)
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
		pgDSN = runconfig.EnvOr("BUDGIE_POSTGRES_DSN", "")
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
