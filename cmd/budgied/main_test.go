package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
)

func TestResolveWebRootUsesExplicitPath(t *testing.T) {
	if got := resolveWebRoot("/tmp/custom-web"); got != "/tmp/custom-web" {
		t.Fatalf("expected explicit web root to win, got %q", got)
	}
}

func TestValidateConfiguredJWTSecret(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		ok     bool
	}{
		{"placeholder rejected", "change-me-in-production", false},
		{"too short rejected", "short", false},
		{"31 bytes rejected", strings.Repeat("a", 31), false},
		{"32 bytes accepted", strings.Repeat("a", 32), true},
		{"long accepted", strings.Repeat("a", 64), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfiguredJWTSecret(tc.secret)
			if tc.ok && err != nil {
				t.Fatalf("expected %q to be accepted, got error: %v", tc.secret, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected %q to be rejected, got nil", tc.secret)
			}
		})
	}
}

func TestResolveJWTSecretEmptyGeneratesEphemeral(t *testing.T) {
	// An empty configuration yields a random ephemeral secret (no exit), and
	// successive calls differ (proving it is freshly generated, not a default).
	a := resolveJWTSecret("")
	b := resolveJWTSecret("")
	if len(a) < minJWTSecretLen {
		t.Fatalf("ephemeral secret too short: %d", len(a))
	}
	if string(a) == string(b) {
		t.Fatal("expected distinct ephemeral secrets on each call")
	}
	if string(a) == "change-me-in-production" {
		t.Fatal("ephemeral secret must not be the placeholder")
	}
}

func TestHasWebIndex(t *testing.T) {
	root := t.TempDir()
	if hasWebIndex(root) {
		t.Fatalf("expected empty dir to have no web index")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if !hasWebIndex(root) {
		t.Fatalf("expected index.html to be detected")
	}
}

func TestObfuscateDSN(t *testing.T) {
	// obfuscateDSN strips credentials AND host:port, retaining only the scheme
	// prefix and DB path — more aggressive than just hiding the password.
	tests := []struct {
		in   string
		want string
	}{
		{
			in:   "postgres://alice:secret@db.example.com:5432/budgie",
			want: "postgres://****/budgie",
		},
		{
			in:   "postgres://user:pass@127.0.0.1:5432/mydb",
			want: "postgres://****/mydb",
		},
		// DSN without '@' (e.g. plain host:port/db without auth)
		{in: "localhost:5432/budgie", want: "[redacted]"},
		// DSN with '@' but no DB path component
		{
			in:   "postgres://u:p@host",
			want: "postgres://****/",
		},
	}
	for _, tt := range tests {
		got := obfuscateDSN(tt.in)
		if got != tt.want {
			t.Errorf("obfuscateDSN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveStorage(t *testing.T) {
	// Clean any pre-existing env var.
	os.Unsetenv("BUDGIE_POSTGRES_DSN")

	// Default: no DSN, no explicit storage → sqlite
	if s, dsn := resolveStorage("sqlite", ""); s != "sqlite" || dsn != "" {
		t.Errorf("default: got storage=%q dsn=%q", s, dsn)
	}

	// Explicit -storage postgres with flag DSN
	if s, dsn := resolveStorage("postgres", "postgres://u:p@host/db"); s != "postgres" || dsn != "postgres://u:p@host/db" {
		t.Errorf("explicit postgres flag: got storage=%q dsn=%q", s, dsn)
	}

	// Backwards compat: DSN via flag, storage still default "sqlite" → inferred postgres
	if s, dsn := resolveStorage("sqlite", "postgres://u:p@host/db"); s != "postgres" || dsn != "postgres://u:p@host/db" {
		t.Errorf("backwards compat (flag dsn): got storage=%q dsn=%q", s, dsn)
	}

	// DSN via environment variable
	os.Setenv("BUDGIE_POSTGRES_DSN", "postgres://env:secret@envhost/db")
	defer os.Unsetenv("BUDGIE_POSTGRES_DSN")
	if s, dsn := resolveStorage("sqlite", ""); s != "postgres" || dsn != "postgres://env:secret@envhost/db" {
		t.Errorf("env var: got storage=%q dsn=%q", s, dsn)
	}

	// Flag DSN overrides env var (flag is set → env not consulted)
	if s, dsn := resolveStorage("postgres", "postgres://flag:x@flaghost/db"); s != "postgres" || dsn != "postgres://flag:x@flaghost/db" {
		t.Errorf("flag overrides env: got storage=%q dsn=%q", s, dsn)
	}
}

func TestParseRolesAllowsGatewayAndWriter(t *testing.T) {
	roles := parseRoles("api, gateway, writer")
	if !roles["api"] || !roles["gateway"] || !roles["writer"] {
		t.Fatalf("roles = %+v, want api, gateway, and writer", roles)
	}
}

func TestNormalizeLogBackend(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"off", ""},
		{" none ", ""},
		{"memory", "memory"},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
	}
	for _, tt := range tests {
		if got := normalizeLogBackend(tt.in); got != tt.want {
			t.Fatalf("normalizeLogBackend(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeEventLogPromotionReadinessBackend(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "nats"},
		{"off", "nats"},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
		{"kafka", "kafka"},
	}
	for _, tt := range tests {
		if got := normalizeEventLogPromotionReadinessBackend(tt.in); got != tt.want {
			t.Fatalf("normalizeEventLogPromotionReadinessBackend(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeProjectionRebuildSource(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "sql"},
		{"SQL", "sql"},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
		{"kafka", "kafka"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		if got := normalizeProjectionRebuildSource(tt.in); got != tt.want {
			t.Fatalf("normalizeProjectionRebuildSource(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSupportedLogBackend(t *testing.T) {
	for _, mode := range []string{"", "memory", "nats"} {
		if !isSupportedLogBackend(mode) {
			t.Fatalf("mode %q should be supported", mode)
		}
	}
	if isSupportedLogBackend("kafka") {
		t.Fatalf("generic log backend should not accept kafka without a role-specific adapter")
	}
	if !isSupportedCommandLogBackend("kafka") {
		t.Fatalf("command-log kafka should be accepted for SQL-executor wiring")
	}
	if !isSupportedEventLogShadowBackend("kafka") {
		t.Fatalf("event-log shadow kafka should be accepted for Kafka shadow wiring")
	}
}

func TestCommandLogWorkerSupportedBackendMessagesIncludeKafka(t *testing.T) {
	if got := supportedCommandLogWorkerBackends(); got != "memory,nats,kafka" {
		t.Fatalf("supportedCommandLogWorkerBackends() = %q, want memory,nats,kafka", got)
	}
	if got := supportedNativeCommandLogWorkerBackends(); got != "nats,kafka" {
		t.Fatalf("supportedNativeCommandLogWorkerBackends() = %q, want nats,kafka", got)
	}
}

func TestOpenProjectionRebuildEventStoreSupportsKafka(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "rebuild-kafka.db"))
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer c.DB.Close()

	source, store, cleanup, err := openProjectionRebuildEventStore(t.Context(), projectionRebuildEventStoreOptions{
		Source:               "redpanda",
		KafkaConfig:          kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		KafkaEventPartitions: 32,
		ClientID:             "rebuild-test",
		DB:                   c.DB,
	})
	defer cleanup()
	if err != nil {
		t.Fatalf("openProjectionRebuildEventStore: %v", err)
	}
	if source != "kafka" {
		t.Fatalf("source = %q, want kafka", source)
	}
	if store == nil {
		t.Fatalf("store = nil")
	}
	if _, ok := store.(core.EventPartitionOffsetLister); !ok {
		t.Fatalf("store = %T, want event partition offset lister", store)
	}
}

func TestValidatePendingKafkaBackendsRequireRuntimeConfig(t *testing.T) {
	if err := validateKafkaCommandLogBackend("nats", kafkaconn.RuntimeConfig{}, 0); err != nil {
		t.Fatalf("validate nats mode: %v", err)
	}
	if err := validateKafkaCommandLogBackend("kafka", kafkaconn.RuntimeConfig{}, 32); err == nil || !strings.Contains(err.Error(), "broker list is required") {
		t.Fatalf("validate command-log kafka without brokers err = %v, want broker-list error", err)
	}
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", "")
	if err := validateKafkaCommandLogBackend("kafka", config, 32); err != nil {
		t.Fatalf("validate command-log kafka with config: %v", err)
	}
	if err := validateKafkaCommandLogBackend("kafka", config, 0); err == nil || !strings.Contains(err.Error(), "kafka-command-partitions") {
		t.Fatalf("validate command-log kafka without partitions err = %v, want partition-count error", err)
	}
	if err := validateKafkaEventShadowBackend("kafka", config, 0); err == nil || !strings.Contains(err.Error(), "kafka-event-partitions") {
		t.Fatalf("validate event-log kafka shadow without partitions err = %v, want event partition error", err)
	}
	if err := validateKafkaEventShadowBackend("kafka", config, 32); err != nil {
		t.Fatalf("validate event-log kafka shadow with partitions: %v", err)
	}
}

func TestValidatePendingKafkaNativeWorkerRequiresDistinctTopics(t *testing.T) {
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "")
	err := validateKafkaCommandWorkerBackend("kafka", "native", config, 32, 32)
	if err == nil || !strings.Contains(err.Error(), "command and event topics must be distinct") {
		t.Fatalf("validate native kafka worker err = %v, want distinct topic error", err)
	}
	if err := validateKafkaCommandWorkerBackend("kafka", "native", kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""), 32, 0); err == nil || !strings.Contains(err.Error(), "kafka-event-partitions") {
		t.Fatalf("validate native kafka worker without event partitions err = %v, want event partition error", err)
	}
	if err := validateKafkaCommandWorkerBackend("kafka", "native", kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""), 32, 32); err != nil {
		t.Fatalf("validate native kafka worker with distinct topics and partitions: %v", err)
	}
	if err := validateKafkaCommandWorkerBackend("kafka", "sql", config, 32, 0); err != nil {
		t.Fatalf("validate sql kafka worker only needs command log config: %v", err)
	}
}

func TestOpenKafkaCommandLogBuildsIndexedCommandLog(t *testing.T) {
	index := core.NewSQLCommandLogPartitionIndex(nil)
	log, cleanup, err := openKafkaCommandLog(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		32,
		"writer-a",
		index,
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("openKafkaCommandLog: %v", err)
	}
	if _, ok := log.(*core.IndexedCommandLog); !ok {
		t.Fatalf("log = %T, want *core.IndexedCommandLog", log)
	}
	if _, ok := log.(core.CommandPartitionOffsetLister); !ok {
		t.Fatalf("log = %T, want command partition offset lister", log)
	}
}

func TestOpenKafkaNativeCommandLogBuildsTransactionBinder(t *testing.T) {
	index := core.NewSQLCommandLogPartitionIndex(nil)
	log, binder, cleanup, err := openKafkaNativeCommandLog(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		32,
		32,
		"writer-a",
		index,
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("openKafkaNativeCommandLog: %v", err)
	}
	if _, ok := log.(*core.IndexedCommandLog); !ok {
		t.Fatalf("log = %T, want *core.IndexedCommandLog", log)
	}
	if binder == nil {
		t.Fatalf("binder = nil, want transaction binder")
	}
	c, err := core.New(filepath.Join(t.TempDir(), "native-kafka.db"))
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer c.DB.Close()
	transactions, err := binder(c.DB)
	if err != nil {
		t.Fatalf("bind transactions: %v", err)
	}
	if transactions == nil {
		t.Fatalf("transactions = nil")
	}
}

func TestNormalizeEventStoreProjectionMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"off", ""},
		{" none ", ""},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
	}
	for _, tt := range tests {
		if got := normalizeEventStoreProjectionMode(tt.in); got != tt.want {
			t.Fatalf("normalizeEventStoreProjectionMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSupportedEventStoreProjectionMode(t *testing.T) {
	for _, mode := range []string{"", "nats", "kafka"} {
		if !isSupportedEventStoreProjectionMode(mode) {
			t.Fatalf("mode %q should be supported", mode)
		}
	}
	if isSupportedEventStoreProjectionMode("memory") {
		t.Fatalf("memory should not be accepted for broker event-store projection")
	}
}

func TestValidateKafkaEventProjectionBackendRequiresConfigAndPartitions(t *testing.T) {
	if err := validateKafkaEventProjectionBackend("nats", kafkaconn.RuntimeConfig{}, 0); err != nil {
		t.Fatalf("validate nats projection: %v", err)
	}
	if err := validateKafkaEventProjectionBackend("kafka", kafkaconn.RuntimeConfig{}, 32); err == nil || !strings.Contains(err.Error(), "broker list is required") {
		t.Fatalf("validate kafka projection without brokers err = %v, want broker-list error", err)
	}
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "")
	if err := validateKafkaEventProjectionBackend("kafka", config, 0); err == nil || !strings.Contains(err.Error(), "kafka-event-partitions") {
		t.Fatalf("validate kafka projection without event partitions err = %v, want event partition error", err)
	}
	if err := validateKafkaEventProjectionBackend("kafka", config, 32); err != nil {
		t.Fatalf("validate kafka projection: %v", err)
	}
}

func TestOpenKafkaEventProjectionStoreBuildsBrokerEventStore(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "event-projection.db"))
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer c.DB.Close()
	store, cleanup, err := openKafkaEventProjectionStore(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		32,
		"event-projector-a",
		c.DB,
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("openKafkaEventProjectionStore: %v", err)
	}
	if store == nil {
		t.Fatalf("store = nil")
	}
}

func TestOpenEventLogPromotionReadinessStoreBuildsKafkaCandidate(t *testing.T) {
	c, err := core.New(filepath.Join(t.TempDir(), "event-promotion.db"))
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer c.DB.Close()
	store, cleanup, err := openEventLogPromotionReadinessStore(
		t.Context(),
		"redpanda",
		"",
		"",
		0,
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		32,
		"event-promotion-a",
		c.DB,
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("openEventLogPromotionReadinessStore: %v", err)
	}
	if store == nil {
		t.Fatalf("store = nil")
	}
	if _, ok := store.(core.EventPartitionLister); !ok {
		t.Fatalf("store = %T, want event partition lister", store)
	}
}

func TestOpenKafkaEventShadowStoreBuildsBrokerEventStore(t *testing.T) {
	store, cleanup, err := openKafkaEventShadowStore(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		32,
		"event-shadow-a",
	)
	defer cleanup()
	if err != nil {
		t.Fatalf("openKafkaEventShadowStore: %v", err)
	}
	if store == nil {
		t.Fatalf("store = nil")
	}
	if _, cleanup, err := openKafkaEventShadowStore(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		0,
		"event-shadow-a",
	); err == nil || !strings.Contains(err.Error(), "kafka-event-partitions") {
		defer cleanup()
		t.Fatalf("openKafkaEventShadowStore without partitions err = %v, want partition error", err)
	}
}

func TestValidateNativeCommandEventStreams(t *testing.T) {
	if err := validateNativeCommandEventStreams("nats", "native", "BUDGIE_COMMAND_LOG", "BUDGIE_EVENT_LOG"); err != nil {
		t.Fatalf("validate distinct native command/event streams: %v", err)
	}
	if err := validateNativeCommandEventStreams("memory", "native", "SAME", "SAME"); err != nil {
		t.Fatalf("non-NATS worker should not require distinct streams: %v", err)
	}
	err := validateNativeCommandEventStreams("nats", "native", "BUDGIE_LOAD", " BUDGIE_LOAD ")
	if err == nil || !strings.Contains(err.Error(), "distinct command and event streams") {
		t.Fatalf("shared native command/event streams err = %v, want distinct-stream error", err)
	}
}

func TestValidateSameProcessNativeWriterProjectionStreams(t *testing.T) {
	roles := map[string]bool{"writer": true, "worker": true}
	if err := validateSameProcessNativeWriterProjectionStreams(roles, "nats", "native", "nats", "BUDGIE_EVENT_LOG", " BUDGIE_EVENT_LOG "); err != nil {
		t.Fatalf("validate matching writer/projector event stream: %v", err)
	}
	err := validateSameProcessNativeWriterProjectionStreams(roles, "nats", "native", "nats", "BUDGIE_EVENT_LOG_WRITER", "BUDGIE_EVENT_LOG_PROJECTOR")
	if err == nil || !strings.Contains(err.Error(), "same-process native writer/projector") {
		t.Fatalf("mismatched writer/projector streams err = %v, want mismatch error", err)
	}
	if err := validateSameProcessNativeWriterProjectionStreams(map[string]bool{"writer": true}, "nats", "native", "", "WRITER_EVENTS", "PROJECTOR_EVENTS"); err != nil {
		t.Fatalf("split projector nodes should be allowed: %v", err)
	}
	if err := validateSameProcessNativeWriterProjectionStreams(map[string]bool{"worker": true}, "", "sql", "nats", "WRITER_EVENTS", "PROJECTOR_EVENTS"); err != nil {
		t.Fatalf("projector-only nodes should be allowed: %v", err)
	}
}

func TestNormalizeCommandLogWorkerExecutor(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "sql"},
		{"Postgres", "sql"},
		{"NATIVE", "native"},
		{" broker-native ", "native"},
		{"event-transaction", "native"},
	}
	for _, tt := range tests {
		if got := normalizeCommandLogWorkerExecutor(tt.in); got != tt.want {
			t.Fatalf("normalizeCommandLogWorkerExecutor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSupportedCommandLogWorkerExecutor(t *testing.T) {
	for _, mode := range []string{"sql", "native"} {
		if !isSupportedCommandLogWorkerExecutor(mode) {
			t.Fatalf("executor %q should be supported", mode)
		}
	}
	if isSupportedCommandLogWorkerExecutor("wasm") {
		t.Fatalf("wasm should not be accepted until it is wired explicitly")
	}
}

func TestNormalizeCommandLogWorkerOwnership(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "sql-lease"},
		{"sql", "sql-lease"},
		{"lease", "sql-lease"},
		{"hash", "hash-assignment"},
		{"assignment", "hash-assignment"},
		{"NATS", "nats-kv"},
		{"jetstream-kv", "nats-kv"},
	}
	for _, tt := range tests {
		if got := normalizeCommandLogWorkerOwnership(tt.in); got != tt.want {
			t.Fatalf("normalizeCommandLogWorkerOwnership(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSupportedCommandLogWorkerOwnership(t *testing.T) {
	for _, mode := range []string{"sql-lease", "hash-assignment", "nats-kv"} {
		if !isSupportedCommandLogWorkerOwnership(mode) {
			t.Fatalf("ownership mode %q should be supported", mode)
		}
	}
	if isSupportedCommandLogWorkerOwnership("etcd") {
		t.Fatalf("etcd should not be accepted until it is wired explicitly")
	}
}

func TestNormalizeSideStoreBackends(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"counter", normalizeCounterStoreBackend},
		{"presence", normalizePresenceStoreBackend},
		{"chat", normalizeChatStoreBackend},
	}
	for _, tt := range tests {
		if got := tt.fn(""); got != "sql" {
			t.Fatalf("%s empty backend = %q, want sql", tt.name, got)
		}
		if got := tt.fn(" Postgres "); got != "sql" {
			t.Fatalf("%s postgres backend = %q, want sql", tt.name, got)
		}
		if got := tt.fn("jetstream-kv"); got != "nats-kv" {
			t.Fatalf("%s jetstream backend = %q, want nats-kv", tt.name, got)
		}
	}
}

func TestIsSupportedSideStoreBackends(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) bool
	}{
		{"counter", isSupportedCounterStoreBackend},
		{"presence", isSupportedPresenceStoreBackend},
		{"chat", isSupportedChatStoreBackend},
	}
	for _, tt := range tests {
		for _, mode := range []string{"sql", "nats-kv"} {
			if !tt.fn(mode) {
				t.Fatalf("%s backend %q should be supported", tt.name, mode)
			}
		}
		if tt.fn("redis") {
			t.Fatalf("%s backend redis should not be accepted until it is wired explicitly", tt.name)
		}
	}
}

func TestNormalizeReadCacheBackend(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"off", ""},
		{"disabled", ""},
		{"MEM", "memory"},
		{" memory ", "memory"},
		{"Redis", "redis"},
		{"dragonfly", "dragonfly"},
	}
	for _, tt := range tests {
		if got := normalizeReadCacheBackend(tt.in); got != tt.want {
			t.Fatalf("normalizeReadCacheBackend(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSupportedReadCacheBackend(t *testing.T) {
	for _, mode := range []string{"", "memory", "redis"} {
		if !isSupportedReadCacheBackend(mode) {
			t.Fatalf("read-cache backend %q should be supported", mode)
		}
	}
	if isSupportedReadCacheBackend("memcached") {
		t.Fatalf("memcached should not be accepted until it is wired explicitly")
	}
}

func TestOpenReadCache(t *testing.T) {
	cache, cleanup, err := openReadCache(t.Context(), "", "", "", time.Minute)
	defer cleanup()
	if err != nil {
		t.Fatalf("open disabled read cache: %v", err)
	}
	if cache != nil {
		t.Fatalf("disabled read cache = %T, want nil", cache)
	}

	cache, cleanup, err = openReadCache(t.Context(), "memory", "", "", time.Minute)
	defer cleanup()
	if err != nil {
		t.Fatalf("open memory read cache: %v", err)
	}
	if _, ok := cache.(*core.MemoryReadCache); !ok {
		t.Fatalf("memory read cache = %T, want *core.MemoryReadCache", cache)
	}

	cache, cleanup, err = openReadCache(t.Context(), "redis", "redis://:secret@redis.internal:6379/3", "budgie:test", time.Minute)
	defer cleanup()
	if err != nil {
		t.Fatalf("open redis read cache: %v", err)
	}
	if cache == nil {
		t.Fatalf("redis read cache = nil")
	}

	if _, cleanup, err := openReadCache(t.Context(), "redis", "", "budgie:test", time.Minute); err == nil || !strings.Contains(err.Error(), "URL or address is required") {
		defer cleanup()
		t.Fatalf("open redis read cache without URL err = %v, want URL error", err)
	}
}

func TestApplyDerivedViewProcessorSelectionEnablesGroups(t *testing.T) {
	var asyncPostSearch bool
	var postSearch bool
	var digestSearch bool
	var latestFeed bool
	var residentFeed bool
	var boardRankings bool
	var threadRankings bool
	var replyRankings bool
	var userRankings bool
	var blessingRankings bool
	var archiveRankings bool

	views, err := applyDerivedViewProcessorSelection(" search, feeds, rankings ", derivedViewProcessorFlags{
		asyncPostSearch:           &asyncPostSearch,
		postSearchProcessor:       &postSearch,
		digestSearchProcessor:     &digestSearch,
		latestFeedProcessor:       &latestFeed,
		residentFeedProcessor:     &residentFeed,
		boardRankingsProcessor:    &boardRankings,
		threadRankingsProcessor:   &threadRankings,
		replyRankingsProcessor:    &replyRankings,
		userRankingsProcessor:     &userRankings,
		blessingRankingsProcessor: &blessingRankings,
		archiveRankingsProcessor:  &archiveRankings,
	})
	if err != nil {
		t.Fatalf("apply derived view processor selection: %v", err)
	}
	for _, want := range []string{
		core.DerivedViewPostSearch,
		core.DerivedViewDigestSearch,
		core.DerivedViewLatestFeed,
		core.DerivedViewResidentFeed,
		core.DerivedViewBoardRankings,
		core.DerivedViewThreadRankings,
		core.DerivedViewReplyRankings,
		core.DerivedViewUserRankings,
		core.DerivedViewBlessingRankings,
		core.DerivedViewArchiveRankings,
	} {
		if !containsString(views, want) {
			t.Fatalf("resolved views %v missing %s", views, want)
		}
	}
	if !asyncPostSearch || !postSearch || !digestSearch || !latestFeed || !residentFeed ||
		!boardRankings || !threadRankings || !replyRankings || !userRankings || !blessingRankings || !archiveRankings {
		t.Fatalf("processor flags not fully enabled: asyncPost=%v post=%v digest=%v latest=%v resident=%v board=%v thread=%v reply=%v user=%v blessing=%v archive=%v",
			asyncPostSearch, postSearch, digestSearch, latestFeed, residentFeed, boardRankings, threadRankings, replyRankings, userRankings, blessingRankings, archiveRankings)
	}
}

func TestApplyDerivedViewProcessorSelectionEnablesCommunityHistory(t *testing.T) {
	var communityStats bool
	var asyncCommunityStatHistory bool

	views, err := applyDerivedViewProcessorSelection("community", derivedViewProcessorFlags{
		communityStatsProcessor:   &communityStats,
		asyncCommunityStatHistory: &asyncCommunityStatHistory,
	})
	if err != nil {
		t.Fatalf("apply community processors: %v", err)
	}
	if !containsString(views, core.DerivedViewCommunityStats) || !containsString(views, core.DerivedViewCommunityStatHistory) {
		t.Fatalf("community processor views = %v, want stats and stat-history", views)
	}
	if !communityStats || !asyncCommunityStatHistory {
		t.Fatalf("community flags = stats:%v history:%v, want both enabled", communityStats, asyncCommunityStatHistory)
	}
}

func TestApplyDerivedViewProcessorSelectionRejectsUnknownView(t *testing.T) {
	if _, err := applyDerivedViewProcessorSelection("search,unknown", derivedViewProcessorFlags{}); err == nil {
		t.Fatal("unknown derived view processor selection succeeded, want error")
	}
}

func TestParseCommandLogWorkerGroupMembers(t *testing.T) {
	got := parseCommandLogWorkerGroupMembers("writer-b, writer-a,writer-a,,")
	want := []string{"writer-a", "writer-b"}
	if len(got) != len(want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("members = %v, want %v", got, want)
		}
	}
}

func TestParseCommandLogWorkerAssignmentOverrides(t *testing.T) {
	got, err := parseCommandLogWorkerAssignmentOverrides("thread/thr_hot#reply-0=writer-b, board/general = writer-a,thread/thr_hot#reply-0=writer-c")
	if err != nil {
		t.Fatalf("parse command worker assignment overrides: %v", err)
	}
	if got[core.LogPartition{Kind: "thread", Key: "thr_hot#reply-0"}] != "writer-c" {
		t.Fatalf("thread override = %q, want last writer-c", got[core.LogPartition{Kind: "thread", Key: "thr_hot#reply-0"}])
	}
	if got[core.LogPartition{Kind: "board", Key: "general"}] != "writer-a" {
		t.Fatalf("board override = %q, want writer-a", got[core.LogPartition{Kind: "board", Key: "general"}])
	}
}

func TestParseCommandLogWorkerAssignmentOverridesRejectsInvalidSpecs(t *testing.T) {
	for _, raw := range []string{
		"thread/thr_hot#reply-0",
		"thread=writer-a",
		"/thr_hot=writer-a",
		"thread/=writer-a",
		"thread/thr_hot=",
	} {
		if _, err := parseCommandLogWorkerAssignmentOverrides(raw); err == nil {
			t.Fatalf("parseCommandLogWorkerAssignmentOverrides(%q) succeeded, want error", raw)
		}
	}
}

func TestParseHotThreadSplits(t *testing.T) {
	got, err := parseHotThreadSplits("thr_hot=4, thr_other = 8,thr_hot=6")
	if err != nil {
		t.Fatalf("parse hot thread splits: %v", err)
	}
	if len(got) != 2 || got["thr_hot"] != 6 || got["thr_other"] != 8 {
		t.Fatalf("splits = %+v, want hot override to 6 and other 8", got)
	}
}

func TestParseHotThreadSplitsRejectsInvalidSpecs(t *testing.T) {
	for _, raw := range []string{"thr_hot", "=4", "thr_hot=one", "thr_hot=1"} {
		if _, err := parseHotThreadSplits(raw); err == nil {
			t.Fatalf("parseHotThreadSplits(%q) succeeded, want error", raw)
		}
	}
}

func TestHasLocalCommandProducerRole(t *testing.T) {
	if !hasLocalCommandProducerRole(map[string]bool{"api": true}, false, false) {
		t.Fatalf("api role should produce commands")
	}
	if !hasLocalCommandProducerRole(map[string]bool{"gateway": true}, false, false) {
		t.Fatalf("gateway role should be treated as a command producer for command-log ownership guards")
	}
	if !hasLocalCommandProducerRole(map[string]bool{"worker": true}, true, false) {
		t.Fatalf("worker role with auto-stats should produce commands")
	}
	if !hasLocalCommandProducerRole(map[string]bool{"worker": true}, false, true) {
		t.Fatalf("worker role with counter checkpoints should produce commands")
	}
	if hasLocalCommandProducerRole(map[string]bool{"worker": true}, false, false) {
		t.Fatalf("worker role without local producers should not be treated as a local command producer")
	}
	if hasLocalCommandProducerRole(map[string]bool{"writer": true}, true, true) {
		t.Fatalf("writer role should not produce local commands by itself")
	}
}
