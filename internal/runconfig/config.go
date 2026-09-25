package runconfig

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

var schemaNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	PreflightTargetPostgres = "postgres"
	PreflightTargetNATS     = "nats"
	PreflightTargetKafka    = "kafka"
)

func EnvOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func ValueOrEnv(value, key string) string {
	if value != "" {
		return value
	}
	return os.Getenv(key)
}

func EnvBool(key string, fallback bool) bool {
	value := normalizedToken(os.Getenv(key))
	switch value {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func EnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var out int
	if _, err := fmt.Sscanf(raw, "%d", &out); err != nil || out == 0 {
		return fallback
	}
	return out
}

func EnvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	out, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return out
}

func MinDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func normalizedToken(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func NormalizeBackendAlias(raw string) string {
	backend := normalizedToken(raw)
	switch backend {
	case "jetstream":
		return "nats"
	case "redpanda":
		return "kafka"
	default:
		return backend
	}
}

func NormalizeOptionalLogBackend(raw string) string {
	backend := NormalizeBackendAlias(raw)
	switch backend {
	case "", "off", "none":
		return ""
	default:
		return backend
	}
}

func IsSupportedOptionalBrokerLogBackend(mode string) bool {
	return mode == "" || mode == "memory" || mode == "nats" || mode == "kafka"
}

func SupportedOptionalBrokerLogBackends() string {
	return "off,memory,nats,kafka"
}

func SupportedBrokerLogBackends() string {
	return "memory,nats,kafka"
}

func IsSupportedOptionalNATSKafkaBackend(mode string) bool {
	return mode == "" || mode == "nats" || mode == "kafka"
}

func SupportedOptionalNATSKafkaBackends() string {
	return "off,nats,kafka"
}

func SupportedNATSKafkaBackends() string {
	return "nats,kafka"
}

func NormalizeSQLOrNATSKVBackend(raw string) string {
	mode := normalizedToken(raw)
	switch mode {
	case "", "sql", "postgres", "postgresql":
		return "sql"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return mode
	}
}

func IsSupportedSQLOrNATSKVBackend(mode string) bool {
	return mode == "sql" || mode == "nats-kv"
}

func SupportedSQLOrNATSKVBackends() string {
	return "sql,nats-kv"
}

func NormalizeReadCacheBackend(raw string) string {
	backend := normalizedToken(raw)
	switch backend {
	case "", "off", "none", "disabled":
		return ""
	case "memory", "mem":
		return "memory"
	case "redis":
		return "redis"
	default:
		return backend
	}
}

func IsSupportedReadCacheBackend(mode string) bool {
	return mode == "" || mode == "memory" || mode == "redis"
}

func SupportedReadCacheBackends() string {
	return "off,memory,redis"
}

func NormalizePostSearchIndexBackend(raw string) string {
	backend := normalizedToken(raw)
	switch backend {
	case "", "sql", "sql-fts", "sqlite", "postgres", "postgresql":
		return "sql-fts"
	case "meili", "meilisearch":
		return "meilisearch"
	default:
		return backend
	}
}

func IsSupportedPostSearchIndexBackend(mode string) bool {
	return mode == "sql-fts" || mode == "meilisearch"
}

func SupportedPostSearchIndexBackends() string {
	return "sql-fts,meilisearch"
}

func NormalizeCommandLogWorkerOwnership(raw string) string {
	mode := normalizedToken(raw)
	switch mode {
	case "", "sql", "lease", "sql-lease":
		return "sql-lease"
	case "hash", "assignment", "hash-assignment":
		return "hash-assignment"
	case "nats", "nats-kv", "jetstream", "jetstream-kv":
		return "nats-kv"
	default:
		return mode
	}
}

func IsSupportedCommandLogWorkerOwnership(mode string) bool {
	return mode == "sql-lease" || mode == "hash-assignment" || mode == "nats-kv"
}

func SupportedCommandLogWorkerOwnershipModes() string {
	return "sql-lease,hash-assignment,nats-kv"
}

func NormalizeEventLogPromotionReadinessBackend(raw string) string {
	mode := NormalizeOptionalLogBackend(raw)
	if mode == "" {
		return "nats"
	}
	return mode
}

func SupportedEventLogPromotionReadinessBackends() string {
	return "nats,kafka"
}

func NormalizeProjectionRebuildSource(raw string) string {
	source := NormalizeBackendAlias(raw)
	switch source {
	case "", "sql":
		return "sql"
	default:
		return source
	}
}

func SupportedProjectionRebuildSources() string {
	return "sql,nats,kafka"
}

func SupportedCommandLogExecutors() string {
	return "sql,native"
}

func NormalizePreflightTargets(raw string, defaults []string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return NormalizeOrderedTargets(raw, defaults, preflightTargetOrder(), "preflight")
	}
	targets, err := NormalizeOrderedTargets(raw, nil, preflightTargetOrder(), "preflight")
	if err != nil {
		return nil, err
	}
	if slices.Contains(targets, PreflightTargetNATS) || slices.Contains(targets, PreflightTargetKafka) {
		targets = append([]string{PreflightTargetPostgres}, targets...)
	}
	return NormalizeOrderedTargets("", targets, preflightTargetOrder(), "preflight")
}

func InferPreflightTargets(raw, natsURL, kafkaBrokers, postgresDSN string) ([]string, error) {
	defaults := []string{}
	if strings.TrimSpace(postgresDSN) != "" {
		defaults = append(defaults, PreflightTargetPostgres)
	}
	if strings.TrimSpace(natsURL) != "" {
		defaults = append(defaults, PreflightTargetPostgres, PreflightTargetNATS)
	}
	if strings.TrimSpace(kafkaBrokers) != "" {
		defaults = append(defaults, PreflightTargetPostgres, PreflightTargetKafka)
	}
	return NormalizePreflightTargets(raw, defaults)
}

func TargetSet(targets []string) map[string]bool {
	out := map[string]bool{}
	for _, target := range targets {
		target = normalizedToken(target)
		if target != "" {
			out[target] = true
		}
	}
	return out
}

func preflightTargetOrder() []string {
	return []string{PreflightTargetPostgres, PreflightTargetNATS, PreflightTargetKafka}
}

func NormalizeOrderedTargets(raw string, defaults, allowed []string, label string) ([]string, error) {
	seenAllowed := map[string]bool{}
	normalizedAllowed := []string{}
	for _, target := range allowed {
		target = normalizedToken(target)
		if target == "" || seenAllowed[target] {
			continue
		}
		seenAllowed[target] = true
		normalizedAllowed = append(normalizedAllowed, target)
	}
	allowed = normalizedAllowed
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return normalizeOrderedTargetList(defaults, allowed), nil
	}
	targets := []string{}
	for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		target := normalizedToken(field)
		switch {
		case target == "all":
			targets = append(targets, allowed...)
		case target == "":
		case slices.Contains(allowed, target):
			targets = append(targets, target)
		default:
			return nil, fmt.Errorf("unsupported %s target %q; supported targets: %s,all", strings.TrimSpace(label), field, strings.Join(allowed, ","))
		}
	}
	return normalizeOrderedTargetList(targets, allowed), nil
}

func normalizeOrderedTargetList(in, allowed []string) []string {
	seen := map[string]bool{}
	for _, target := range in {
		target = normalizedToken(target)
		if target == "" || !slices.Contains(allowed, target) {
			continue
		}
		seen[target] = true
	}
	out := []string{}
	for _, target := range allowed {
		if seen[target] {
			out = append(out, target)
		}
	}
	return out
}

func ValidSchemaName(schema string) bool {
	return schemaNamePattern.MatchString(strings.TrimSpace(schema))
}

func DisposableSchemaName(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		prefix = "budgie_load"
	}
	return fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func MaybeDisposablePostgresSchema(postgresDSN, requestedSchema, prefix string) string {
	if strings.TrimSpace(postgresDSN) == "" {
		return ""
	}
	if schema := strings.TrimSpace(requestedSchema); schema != "" {
		return schema
	}
	return DisposableSchemaName(prefix)
}

func WithSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func PreparePostgresSchema(ctx context.Context, db *sql.DB, requestedSchema, prefix string, keep bool, logf func(string, ...any)) (string, func(), error) {
	schema := strings.TrimSpace(requestedSchema)
	if schema == "" {
		schema = DisposableSchemaName(prefix)
	}
	if !ValidSchemaName(schema) {
		return "", func() {}, fmt.Errorf("invalid schema %q; use letters, digits, and underscores, starting with a letter or underscore", schema)
	}
	if db == nil {
		return "", func() {}, fmt.Errorf("nil postgres admin db")
	}
	if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		return "", func() {}, fmt.Errorf("drop old schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		return "", func() {}, fmt.Errorf("create schema: %w", err)
	}
	cleanup := func() {}
	if !keep {
		cleanup = func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil && logf != nil {
				logf("cleanup schema %s: %v", schema, err)
			}
		}
	}
	return schema, cleanup, nil
}
