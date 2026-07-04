package loadtest

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

type CoreConfig struct {
	PostgresDSN       string
	SQLitePath        string
	Schema            string
	SchemaPrefix      string
	KeepSchema        bool
	Logf              func(string, ...any)
	SQLiteTempPattern string
}

type PostgresCoreConfig struct {
	DSN          string
	Schema       string
	SchemaPrefix string
	KeepSchema   bool
	Logf         func(string, ...any)
}

func OpenCore(ctx context.Context, config CoreConfig, options ...core.Option) (*core.Core, func(), error) {
	postgresDSN := strings.TrimSpace(config.PostgresDSN)
	if postgresDSN != "" {
		return OpenPostgresCore(ctx, PostgresCoreConfig{
			DSN:          postgresDSN,
			Schema:       config.Schema,
			SchemaPrefix: config.SchemaPrefix,
			KeepSchema:   config.KeepSchema,
			Logf:         config.Logf,
		}, options...)
	}

	path := strings.TrimSpace(config.SQLitePath)
	tempPath := false
	if path == "" {
		pattern := strings.TrimSpace(config.SQLiteTempPattern)
		if pattern == "" {
			pattern = "budgie-load-*.db"
		}
		f, err := os.CreateTemp("", pattern)
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

func OpenPostgresCore(ctx context.Context, config PostgresCoreConfig, options ...core.Option) (*core.Core, func(), error) {
	dsn := strings.TrimSpace(config.DSN)
	if dsn == "" {
		return nil, func() {}, fmt.Errorf("postgres DSN is required")
	}
	schema := strings.TrimSpace(config.Schema)
	if schema != "" && !runconfig.ValidSchemaName(schema) {
		return nil, func() {}, fmt.Errorf("invalid schema %q; use letters, digits, and underscores, starting with a letter or underscore", schema)
	}
	prefix := strings.TrimSpace(config.SchemaPrefix)
	if prefix == "" {
		prefix = "budgie_load"
	}

	adminDB, err := core.OpenPostgres(dsn)
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		adminDB.Close()
	}
	schemaName, schemaCleanup, err := runconfig.PreparePostgresSchema(ctx, adminDB, schema, prefix, config.KeepSchema, config.Logf)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	previousCleanup := cleanup
	cleanup = func() {
		schemaCleanup()
		previousCleanup()
	}

	c, err := core.NewPostgres(runconfig.WithSearchPath(dsn, schemaName), options...)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return c, cleanup, nil
}
