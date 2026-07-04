package core

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

const (
	sqliteFlavor = iota
	postgresFlavor
)

func PostgresFlavor() int {
	return postgresFlavor
}

// SQLFlavor exposes the normalized flavor for callers that need an explicit check.
func SQLFlavor() int {
	return currentSQLFlavor
}

// SetSQLFlavorForTests sets the active SQL-flavor binder.
//
// This is intentionally narrow-scoped and used mainly by startup paths that
// want to force placeholder rewriting behavior without constructing a full core.
func SetSQLFlavorForTests(flavor int) {
	setSQLFlavor(flavor)
	projections.SetSQLFlavor(flavor)
}

var currentSQLFlavor = sqliteFlavor

func setSQLFlavor(flavor int) {
	currentSQLFlavor = flavor
}

type sqlLike interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func qExec(execable sqlLike, query string, args ...any) (sql.Result, error) {
	return execable.Exec(rebindPlaceholders(query), args...)
}

func qQuery(queryable sqlLike, query string, args ...any) (*sql.Rows, error) {
	return queryable.Query(rebindPlaceholders(query), args...)
}

func qQueryRow(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, query string, args ...any) *sql.Row {
	return queryable.QueryRow(rebindPlaceholders(query), args...)
}

func queryPlaceholders(n int) string {
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

func stringQueryArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func execReturningSeq(tx *sql.Tx, query string, args ...any) (int64, error) {
	query = rebindPlaceholders(query)
	if currentSQLFlavor == postgresFlavor {
		if !strings.Contains(strings.ToUpper(query), " RETURNING ") {
			query = query + " RETURNING seq"
		}
		var seq int64
		if err := qQueryRow(tx, query, args...).Scan(&seq); err != nil {
			return 0, err
		}
		return seq, nil
	}
	res, err := qExec(tx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func rebindPlaceholders(query string) string {
	if currentSQLFlavor != postgresFlavor {
		return query
	}
	query = rewriteInsertOrIgnoreForPostgres(query)
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteString("$")
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// rewriteInsertOrIgnoreForPostgres translates SQLite's "INSERT OR IGNORE INTO"
// into the portable "INSERT INTO ... ON CONFLICT DO NOTHING" form that Postgres
// accepts. Applied only in Postgres mode. A no-op if the statement is not an
// INSERT OR IGNORE or already carries an ON CONFLICT clause.
func rewriteInsertOrIgnoreForPostgres(query string) string {
	const clause = "INSERT OR IGNORE"
	idx := strings.Index(strings.ToUpper(query), clause)
	if idx < 0 {
		return query
	}
	query = query[:idx] + "INSERT" + query[idx+len(clause):]
	if strings.Contains(strings.ToUpper(query), "ON CONFLICT") {
		return query
	}
	trimmed := strings.TrimRight(query, " \t\r\n")
	semi := strings.HasSuffix(trimmed, ";")
	trimmed = strings.TrimRight(strings.TrimRight(trimmed, ";"), " \t\r\n")
	query = trimmed + " ON CONFLICT DO NOTHING"
	if semi {
		query += ";"
	}
	return query
}
