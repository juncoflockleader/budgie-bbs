package runconfig

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

type normalizeCase struct {
	in   string
	want string
}

func assertNormalizeStrings(t *testing.T, name string, normalize func(string) string, tests []normalizeCase) {
	t.Helper()
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Fatalf("%s(%q) = %q, want %q", name, tt.in, got, tt.want)
		}
	}
}

func assertSupportedStrings(t *testing.T, name string, supported func(string) bool, values ...string) {
	t.Helper()
	for _, value := range values {
		if !supported(value) {
			t.Fatalf("%s(%q) = false, want true", name, value)
		}
	}
}

func assertUnsupportedStrings(t *testing.T, name string, supported func(string) bool, values ...string) {
	t.Helper()
	for _, value := range values {
		if supported(value) {
			t.Fatalf("%s(%q) = true, want false", name, value)
		}
	}
}

func assertEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func assertStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %+v, want %+v", name, got, want)
	}
}

func TestEnvHelpers(t *testing.T) {
	t.Setenv("BUDGIE_RUNCONFIG_STRING", "value")
	t.Setenv("BUDGIE_RUNCONFIG_VALUE_OR_ENV", "env")
	t.Setenv("BUDGIE_RUNCONFIG_BOOL", "yes")
	t.Setenv("BUDGIE_RUNCONFIG_BOOL_T", "t")
	t.Setenv("BUDGIE_RUNCONFIG_BOOL_FALSE", "off")
	t.Setenv("BUDGIE_RUNCONFIG_BOOL_F", "f")
	t.Setenv("BUDGIE_RUNCONFIG_INT", "42")
	t.Setenv("BUDGIE_RUNCONFIG_INT_ZERO", "0")
	t.Setenv("BUDGIE_RUNCONFIG_DURATION", "2m30s")
	t.Setenv("BUDGIE_RUNCONFIG_DURATION_BAD", "soon")

	assertEqual(t, "EnvOr", EnvOr("BUDGIE_RUNCONFIG_STRING", "fallback"), "value")
	assertEqual(t, "missing EnvOr", EnvOr("BUDGIE_RUNCONFIG_MISSING_STRING", "fallback"), "fallback")
	assertEqual(t, "ValueOrEnv flag", ValueOrEnv("flag", "BUDGIE_RUNCONFIG_VALUE_OR_ENV"), "flag")
	assertEqual(t, "ValueOrEnv env", ValueOrEnv("", "BUDGIE_RUNCONFIG_VALUE_OR_ENV"), "env")
	assertEqual(t, "EnvBool yes", EnvBool("BUDGIE_RUNCONFIG_BOOL", false), true)
	assertEqual(t, "EnvBool t", EnvBool("BUDGIE_RUNCONFIG_BOOL_T", false), true)
	assertEqual(t, "EnvBool off", EnvBool("BUDGIE_RUNCONFIG_BOOL_FALSE", true), false)
	assertEqual(t, "EnvBool f", EnvBool("BUDGIE_RUNCONFIG_BOOL_F", true), false)
	assertEqual(t, "EnvInt", EnvInt("BUDGIE_RUNCONFIG_INT", 7), 42)
	assertEqual(t, "zero EnvInt", EnvInt("BUDGIE_RUNCONFIG_INT_ZERO", 7), 7)
	assertEqual(t, "missing EnvInt", EnvInt("BUDGIE_RUNCONFIG_MISSING", 7), 7)
	assertEqual(t, "EnvDuration", EnvDuration("BUDGIE_RUNCONFIG_DURATION", time.Second), 150*time.Second)
	assertEqual(t, "bad EnvDuration", EnvDuration("BUDGIE_RUNCONFIG_DURATION_BAD", time.Second), time.Second)
	assertEqual(t, "missing EnvDuration", EnvDuration("BUDGIE_RUNCONFIG_DURATION_MISSING", time.Second), time.Second)
}

func TestMinDuration(t *testing.T) {
	assertEqual(t, "MinDuration positive", MinDuration(5*time.Second, 2*time.Second), 2*time.Second)
	assertEqual(t, "MinDuration zero first", MinDuration(0, 2*time.Second), 2*time.Second)
	assertEqual(t, "MinDuration zero second", MinDuration(5*time.Second, 0), 5*time.Second)
	assertEqual(t, "MinDuration both zero", MinDuration(0, 0), 0)
}

func TestSchemaHelpers(t *testing.T) {
	assertEqual(t, "valid schema", ValidSchemaName("budgie_cmdlog_load_123"), true)
	assertEqual(t, "hyphenated schema", ValidSchemaName("bad-schema"), false)
	if got := DisposableSchemaName(" __ "); !strings.HasPrefix(got, "budgie_load_") {
		t.Fatalf("empty generated schema prefix = %q", got)
	}
	assertEqual(t, "schema without dsn", MaybeDisposablePostgresSchema("", "ignored", "budgie_cmdlog_load"), "")
	assertEqual(t, "custom schema", MaybeDisposablePostgresSchema("postgres://example/budgie", " custom_schema ", "budgie_cmdlog_load"), "custom_schema")
	got := MaybeDisposablePostgresSchema("postgres://example/budgie", "", "budgie_cmdlog_load")
	if !strings.HasPrefix(got, "budgie_cmdlog_load_") {
		t.Fatalf("generated schema = %q", got)
	}
}

func TestNormalizeBackends(t *testing.T) {
	assertNormalizeStrings(t, "NormalizeBackendAlias", NormalizeBackendAlias, []normalizeCase{
		{" JetStream ", "nats"},
		{"Redpanda", "kafka"},
		{" off ", "off"},
	})
	assertNormalizeStrings(t, "NormalizeOptionalLogBackend", NormalizeOptionalLogBackend, []normalizeCase{
		{" none ", ""},
		{"memory", "memory"},
		{"Redpanda", "kafka"},
	})
	assertSupportedStrings(t, "IsSupportedOptionalBrokerLogBackend", IsSupportedOptionalBrokerLogBackend, "", "memory", "nats", "kafka")
	assertEqual(t, "SupportedOptionalBrokerLogBackends", SupportedOptionalBrokerLogBackends(), "off,memory,nats,kafka")
	assertEqual(t, "SupportedBrokerLogBackends", SupportedBrokerLogBackends(), "memory,nats,kafka")
	assertSupportedStrings(t, "IsSupportedOptionalNATSKafkaBackend", IsSupportedOptionalNATSKafkaBackend, "", "nats", "kafka")
	assertEqual(t, "SupportedOptionalNATSKafkaBackends", SupportedOptionalNATSKafkaBackends(), "off,nats,kafka")
	assertEqual(t, "SupportedNATSKafkaBackends", SupportedNATSKafkaBackends(), "nats,kafka")
	assertUnsupportedStrings(t, "IsSupportedOptionalNATSKafkaBackend", IsSupportedOptionalNATSKafkaBackend, "memory")
	assertNormalizeStrings(t, "NormalizeSQLOrNATSKVBackend", NormalizeSQLOrNATSKVBackend, []normalizeCase{
		{"", "sql"},
		{" Postgres ", "sql"},
		{"jetstream-kv", "nats-kv"},
	})
	assertSupportedStrings(t, "IsSupportedSQLOrNATSKVBackend", IsSupportedSQLOrNATSKVBackend, "sql", "nats-kv")
	assertUnsupportedStrings(t, "IsSupportedSQLOrNATSKVBackend", IsSupportedSQLOrNATSKVBackend, "redis")
	assertEqual(t, "SupportedSQLOrNATSKVBackends", SupportedSQLOrNATSKVBackends(), "sql,nats-kv")
}

func TestNormalizeReadCacheBackend(t *testing.T) {
	assertNormalizeStrings(t, "NormalizeReadCacheBackend", NormalizeReadCacheBackend, []normalizeCase{
		{"", ""},
		{"off", ""},
		{"disabled", ""},
		{"MEM", "memory"},
		{" memory ", "memory"},
		{"Redis", "redis"},
		{"dragonfly", "dragonfly"},
	})
	assertSupportedStrings(t, "IsSupportedReadCacheBackend", IsSupportedReadCacheBackend, "", "memory", "redis")
	assertUnsupportedStrings(t, "IsSupportedReadCacheBackend", IsSupportedReadCacheBackend, "memcached")
	assertEqual(t, "SupportedReadCacheBackends", SupportedReadCacheBackends(), "off,memory,redis")
}

func TestNormalizePostSearchIndexBackend(t *testing.T) {
	assertNormalizeStrings(t, "NormalizePostSearchIndexBackend", NormalizePostSearchIndexBackend, []normalizeCase{
		{"", "sql-fts"},
		{"SQL", "sql-fts"},
		{"postgres", "sql-fts"},
		{"meili", "meilisearch"},
		{" MeiliSearch ", "meilisearch"},
		{"opensearch", "opensearch"},
	})
	assertSupportedStrings(t, "IsSupportedPostSearchIndexBackend", IsSupportedPostSearchIndexBackend, "sql-fts", "meilisearch")
	assertUnsupportedStrings(t, "IsSupportedPostSearchIndexBackend", IsSupportedPostSearchIndexBackend, "opensearch")
	assertEqual(t, "SupportedPostSearchIndexBackends", SupportedPostSearchIndexBackends(), "sql-fts,meilisearch")
}

func TestNormalizeCommandLogWorkerOwnership(t *testing.T) {
	assertNormalizeStrings(t, "NormalizeCommandLogWorkerOwnership", NormalizeCommandLogWorkerOwnership, []normalizeCase{
		{"", "sql-lease"},
		{"sql", "sql-lease"},
		{"lease", "sql-lease"},
		{"hash", "hash-assignment"},
		{"assignment", "hash-assignment"},
		{"NATS", "nats-kv"},
		{"jetstream-kv", "nats-kv"},
	})
	assertSupportedStrings(t, "IsSupportedCommandLogWorkerOwnership", IsSupportedCommandLogWorkerOwnership, "sql-lease", "hash-assignment", "nats-kv")
	assertUnsupportedStrings(t, "IsSupportedCommandLogWorkerOwnership", IsSupportedCommandLogWorkerOwnership, "etcd")
	assertEqual(t, "SupportedCommandLogWorkerOwnershipModes", SupportedCommandLogWorkerOwnershipModes(), "sql-lease,hash-assignment,nats-kv")
}

func TestNormalizeEventLogPromotionReadinessBackend(t *testing.T) {
	assertNormalizeStrings(t, "NormalizeEventLogPromotionReadinessBackend", NormalizeEventLogPromotionReadinessBackend, []normalizeCase{
		{"", "nats"},
		{"off", "nats"},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
		{"kafka", "kafka"},
	})
	assertEqual(t, "SupportedEventLogPromotionReadinessBackends", SupportedEventLogPromotionReadinessBackends(), "nats,kafka")
}

func TestNormalizeProjectionRebuildSource(t *testing.T) {
	assertNormalizeStrings(t, "NormalizeProjectionRebuildSource", NormalizeProjectionRebuildSource, []normalizeCase{
		{"", "sql"},
		{"SQL", "sql"},
		{"NATS", "nats"},
		{"jetstream", "nats"},
		{"Redpanda", "kafka"},
		{"kafka", "kafka"},
		{"unknown", "unknown"},
	})
	assertEqual(t, "SupportedProjectionRebuildSources", SupportedProjectionRebuildSources(), "sql,nats,kafka")
}

func TestNormalizePreflightTargets(t *testing.T) {
	got, err := NormalizePreflightTargets("", []string{PreflightTargetKafka, PreflightTargetPostgres, PreflightTargetKafka})
	if err != nil {
		t.Fatalf("NormalizePreflightTargets defaults: %v", err)
	}
	assertStringSlice(t, "default targets", got, []string{PreflightTargetPostgres, PreflightTargetKafka})

	got, err = NormalizePreflightTargets("kafka,nats", nil)
	if err != nil {
		t.Fatalf("NormalizePreflightTargets explicit: %v", err)
	}
	assertStringSlice(t, "explicit targets", got, []string{PreflightTargetPostgres, PreflightTargetNATS, PreflightTargetKafka})

	if _, err := NormalizePreflightTargets("redis", nil); err == nil || !strings.Contains(err.Error(), "unsupported preflight target") {
		t.Fatalf("unsupported target err = %v, want validation error", err)
	}
}

func TestInferPreflightTargets(t *testing.T) {
	got, err := InferPreflightTargets("", "nats://nats.internal:4222", "redpanda.internal:9092", "postgres://postgres.internal/budgie")
	if err != nil {
		t.Fatalf("InferPreflightTargets defaults: %v", err)
	}
	assertStringSlice(t, "inferred targets", got, []string{PreflightTargetPostgres, PreflightTargetNATS, PreflightTargetKafka})

	got, err = InferPreflightTargets("kafka", "", "redpanda.internal:9092", "postgres://postgres.internal/budgie")
	if err != nil {
		t.Fatalf("InferPreflightTargets explicit: %v", err)
	}
	assertStringSlice(t, "explicit kafka targets", got, []string{PreflightTargetPostgres, PreflightTargetKafka})

	if _, err := InferPreflightTargets("redis", "", "", ""); err == nil || !strings.Contains(err.Error(), "unsupported preflight target") {
		t.Fatalf("unsupported target err = %v, want validation error", err)
	}
}

func TestTargetSetHelpers(t *testing.T) {
	targets := []string{" Kafka ", "postgres", "kafka", ""}
	got := TargetSet(targets)
	if len(got) != 2 || !got[PreflightTargetPostgres] || !got[PreflightTargetKafka] {
		t.Fatalf("TargetSet = %+v, want normalized postgres+kafka", got)
	}
}

func TestNormalizeOrderedTargets(t *testing.T) {
	got, err := NormalizeOrderedTargets("kafka gateway,kafka", nil, []string{"gateway", "nats", "kafka"}, "bundle manifest")
	if err != nil {
		t.Fatalf("NormalizeOrderedTargets explicit: %v", err)
	}
	assertStringSlice(t, "targets", got, []string{"gateway", "kafka"})

	got, err = NormalizeOrderedTargets("", []string{"kafka", "gateway", "gateway"}, []string{"gateway", "nats", "kafka"}, "bundle manifest")
	if err != nil {
		t.Fatalf("NormalizeOrderedTargets defaults: %v", err)
	}
	assertStringSlice(t, "default targets", got, []string{"gateway", "kafka"})

	got, err = NormalizeOrderedTargets("all", nil, []string{"gateway", "nats", "kafka"}, "bundle manifest")
	if err != nil {
		t.Fatalf("NormalizeOrderedTargets all: %v", err)
	}
	assertStringSlice(t, "all targets", got, []string{"gateway", "nats", "kafka"})

	if _, err := NormalizeOrderedTargets("redis", nil, []string{"gateway", "nats", "kafka"}, "bundle manifest"); err == nil || !strings.Contains(err.Error(), "unsupported bundle manifest target") {
		t.Fatalf("unsupported ordered target err = %v, want validation error", err)
	}
}

func TestWithSearchPath(t *testing.T) {
	assertEqual(t, "plain dsn search path", WithSearchPath("postgres://example/budgie", "schema_a"), "postgres://example/budgie?search_path=schema_a")
	assertEqual(t, "query dsn search path", WithSearchPath("postgres://example/budgie?sslmode=require", "schema_a"), "postgres://example/budgie?sslmode=require&search_path=schema_a")
}

func TestPreparePostgresSchemaRejectsInvalidSchemaBeforeDBUse(t *testing.T) {
	_, cleanup, err := PreparePostgresSchema(context.Background(), nil, "bad-schema", "budgie_load", false, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid schema") {
		t.Fatalf("PreparePostgresSchema err = %v, want invalid schema", err)
	}
	cleanup()
}
