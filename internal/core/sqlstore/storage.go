package sqlstore

import (
	"database/sql"
	"strconv"
	"strings"
)

const (
	SQLiteFlavor = iota
	PostgresFlavor
)

var currentFlavor = SQLiteFlavor

func Flavor() int {
	return currentFlavor
}

func SetFlavor(flavor int) {
	currentFlavor = flavor
}

type SQLLike interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type RowQueryable interface {
	QueryRow(query string, args ...any) *sql.Row
}

func Exec(execable SQLLike, query string, args ...any) (sql.Result, error) {
	return execable.Exec(RebindPlaceholders(query), args...)
}

func Query(queryable SQLLike, query string, args ...any) (*sql.Rows, error) {
	return queryable.Query(RebindPlaceholders(query), args...)
}

func QueryRow(queryable RowQueryable, query string, args ...any) *sql.Row {
	return queryable.QueryRow(RebindPlaceholders(query), args...)
}

func QueryPlaceholders(n int) string {
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

func StringQueryArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func RebindPlaceholders(query string) string {
	if currentFlavor != PostgresFlavor {
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
