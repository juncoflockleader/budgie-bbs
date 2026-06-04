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
