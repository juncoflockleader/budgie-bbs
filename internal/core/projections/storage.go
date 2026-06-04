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

func NowMS() int64 { return time.Now().UnixMilli() }
