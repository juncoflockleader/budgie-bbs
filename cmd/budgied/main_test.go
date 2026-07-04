package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/kafkaconn"
	"github.com/juncoflockleader/budgie-bbs/internal/natsconn"
)

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want containing %q", err, want)
	}
}

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
	requireErrorContains(t, validateKafkaCommandLogBackend("kafka", kafkaconn.RuntimeConfig{}, 32), "broker list is required")
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "", "", "")
	if err := validateKafkaCommandLogBackend("kafka", config, 32); err != nil {
		t.Fatalf("validate command-log kafka with config: %v", err)
	}
	requireErrorContains(t, validateKafkaCommandLogBackend("kafka", config, 0), "kafka-command-partitions")
	requireErrorContains(t, validateKafkaEventShadowBackend("kafka", config, 0), "kafka-event-partitions")
	if err := validateKafkaEventShadowBackend("kafka", config, 32); err != nil {
		t.Fatalf("validate event-log kafka shadow with partitions: %v", err)
	}
}

func TestValidatePendingKafkaNativeWorkerRequiresDistinctTopics(t *testing.T) {
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.log", "budgie.log", "")
	requireErrorContains(t, validateKafkaCommandWorkerBackend("kafka", "native", config, 32, 32), "command and event topics must be distinct")
	requireErrorContains(t,
		validateKafkaCommandWorkerBackend("kafka", "native", kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", ""), 32, 0),
		"kafka-event-partitions")
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

func TestValidateKafkaEventProjectionBackendRequiresConfigAndPartitions(t *testing.T) {
	if err := validateKafkaEventProjectionBackend("nats", kafkaconn.RuntimeConfig{}, 0); err != nil {
		t.Fatalf("validate nats projection: %v", err)
	}
	requireErrorContains(t, validateKafkaEventProjectionBackend("kafka", kafkaconn.RuntimeConfig{}, 32), "broker list is required")
	config := kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "")
	requireErrorContains(t, validateKafkaEventProjectionBackend("kafka", config, 0), "kafka-event-partitions")
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
	_, cleanup, err = openKafkaEventShadowStore(
		t.Context(),
		kafkaconn.RuntimeConfigFromFlags("redpanda:9092", "budgie.commands", "budgie.events", "budgie-writers"),
		0,
		"event-shadow-a",
	)
	defer cleanup()
	requireErrorContains(t, err, "kafka-event-partitions")
}

func TestValidateNativeCommandEventStreams(t *testing.T) {
	if err := validateNativeCommandEventStreams("nats", "native", natsconn.DefaultJetStreamCommandLogStream, natsconn.DefaultJetStreamEventLogStream); err != nil {
		t.Fatalf("validate distinct native command/event streams: %v", err)
	}
	if err := validateNativeCommandEventStreams("memory", "native", "SAME", "SAME"); err != nil {
		t.Fatalf("non-NATS worker should not require distinct streams: %v", err)
	}
	err := validateNativeCommandEventStreams("nats", "native", "BUDGIE_LOAD", " BUDGIE_LOAD ")
	requireErrorContains(t, err, "distinct command and event streams")
}

func TestValidateSameProcessNativeWriterProjectionStreams(t *testing.T) {
	roles := map[string]bool{"writer": true, "worker": true}
	if err := validateSameProcessNativeWriterProjectionStreams(roles, "nats", "native", "nats", natsconn.DefaultJetStreamEventLogStream, " "+natsconn.DefaultJetStreamEventLogStream+" "); err != nil {
		t.Fatalf("validate matching writer/projector event stream: %v", err)
	}
	err := validateSameProcessNativeWriterProjectionStreams(roles, "nats", "native", "nats", "BUDGIE_EVENT_LOG_WRITER", "BUDGIE_EVENT_LOG_PROJECTOR")
	requireErrorContains(t, err, "same-process native writer/projector")
	if err := validateSameProcessNativeWriterProjectionStreams(map[string]bool{"writer": true}, "nats", "native", "", "WRITER_EVENTS", "PROJECTOR_EVENTS"); err != nil {
		t.Fatalf("split projector nodes should be allowed: %v", err)
	}
	if err := validateSameProcessNativeWriterProjectionStreams(map[string]bool{"worker": true}, "", "sql", "nats", "WRITER_EVENTS", "PROJECTOR_EVENTS"); err != nil {
		t.Fatalf("projector-only nodes should be allowed: %v", err)
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

	_, cleanup, err = openReadCache(t.Context(), "redis", "", "budgie:test", time.Minute)
	defer cleanup()
	requireErrorContains(t, err, "URL or address is required")
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

	views, err := applyDerivedViewProcessorSelection(" search, feeds, rankings ",
		derivedViewProcessorSpecialFlags{asyncPostSearch: &asyncPostSearch},
		[]derivedViewProcessorSpec{
			{view: core.DerivedViewPostSearch, enabled: &postSearch},
			{view: core.DerivedViewDigestSearch, enabled: &digestSearch},
			{view: core.DerivedViewLatestFeed, enabled: &latestFeed},
			{view: core.DerivedViewResidentFeed, enabled: &residentFeed},
			{view: core.DerivedViewBoardRankings, enabled: &boardRankings},
			{view: core.DerivedViewThreadRankings, enabled: &threadRankings},
			{view: core.DerivedViewReplyRankings, enabled: &replyRankings},
			{view: core.DerivedViewUserRankings, enabled: &userRankings},
			{view: core.DerivedViewBlessingRankings, enabled: &blessingRankings},
			{view: core.DerivedViewArchiveRankings, enabled: &archiveRankings},
		},
	)
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
		if !slices.Contains(views, want) {
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

	views, err := applyDerivedViewProcessorSelection("community",
		derivedViewProcessorSpecialFlags{asyncCommunityStatHistory: &asyncCommunityStatHistory},
		[]derivedViewProcessorSpec{{view: core.DerivedViewCommunityStats, enabled: &communityStats}},
	)
	if err != nil {
		t.Fatalf("apply community processors: %v", err)
	}
	if !slices.Contains(views, core.DerivedViewCommunityStats) || !slices.Contains(views, core.DerivedViewCommunityStatHistory) {
		t.Fatalf("community processor views = %v, want stats and stat-history", views)
	}
	if !communityStats || !asyncCommunityStatHistory {
		t.Fatalf("community flags = stats:%v history:%v, want both enabled", communityStats, asyncCommunityStatHistory)
	}
}

func TestApplyDerivedViewProcessorSelectionRejectsUnknownView(t *testing.T) {
	if _, err := applyDerivedViewProcessorSelection("search,unknown", derivedViewProcessorSpecialFlags{}, nil); err == nil {
		t.Fatal("unknown derived view processor selection succeeded, want error")
	}
}

func TestMissingDerivedViewProcessorWorkerRoleUsesRegistryOrder(t *testing.T) {
	var latestFeed bool
	processors := []derivedViewProcessorSpec{{label: "latest feed", enabled: &latestFeed}}
	if got := missingDerivedViewProcessorWorkerRole(processors); got != "" {
		t.Fatalf("disabled processor role message = %q, want empty", got)
	}
	latestFeed = true
	if got := missingDerivedViewProcessorWorkerRole(processors); got != "latest feed processor requires the worker role" {
		t.Fatalf("processor role message = %q, want latest feed", got)
	}
}

func TestDerivedViewWatermarkOwnershipConflictUsesRegistry(t *testing.T) {
	var digestSearch bool
	processors := []derivedViewProcessorSpec{{
		view:     core.DerivedViewDigestSearch,
		label:    "digest search",
		flagName: "digest-search-processor",
		enabled:  &digestSearch,
	}}
	conflict, ok := derivedViewWatermarkOwnershipConflict(
		[]string{core.DerivedViewDigestSearch},
		derivedViewProcessorSpecialFlags{},
		processors,
		"sql-fts",
	)
	if ok {
		t.Fatalf("disabled digest conflict = %+v, want none", conflict)
	}
	digestSearch = true
	conflict, ok = derivedViewWatermarkOwnershipConflict(
		[]string{core.DerivedViewDigestSearch},
		derivedViewProcessorSpecialFlags{},
		processors,
		"sql-fts",
	)
	if !ok || conflict.message != "digest search processor ownership cannot use compatibility watermark sync for search.digest" {
		t.Fatalf("digest conflict = %+v/%v, want digest search message", conflict, ok)
	}
	if conflict.hint != "remove search.digest from -derived-view-watermarks and run -digest-search-processor on a worker node" {
		t.Fatalf("digest conflict hint = %q", conflict.hint)
	}

	conflict, ok = derivedViewWatermarkOwnershipConflict(
		[]string{core.DerivedViewPostSearch},
		derivedViewProcessorSpecialFlags{},
		nil,
		"meilisearch",
	)
	if !ok || conflict.message != "post search processor ownership cannot use compatibility watermark sync for search.posts" {
		t.Fatalf("post search index conflict = %+v/%v, want post search message", conflict, ok)
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
