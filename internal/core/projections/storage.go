package projections

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

const (
	sqliteFlavor = iota
	postgresFlavor
)

var currentSQLFlavor = sqliteFlavor

func SetSQLFlavor(flavor int) {
	currentSQLFlavor = flavor
}

type sqlLike interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func QExec(execable sqlLike, query string, args ...any) (sql.Result, error) {
	return execable.Exec(RebindPlaceholders(query), args...)
}

func QQuery(queryable sqlLike, query string, args ...any) (*sql.Rows, error) {
	return queryable.Query(RebindPlaceholders(query), args...)
}

func QQueryRow(queryable sqlLike, query string, args ...any) *sql.Row {
	return queryable.QueryRow(RebindPlaceholders(query), args...)
}

func RebindPlaceholders(query string) string {
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
// into the portable "INSERT INTO ... ON CONFLICT DO NOTHING" form Postgres
// accepts. Applied only in Postgres mode.
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

func NowMS() int64 { return time.Now().UnixMilli() }
