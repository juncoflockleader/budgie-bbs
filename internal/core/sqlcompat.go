package core

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

const (
	sqliteFlavor   = sqlstore.SQLiteFlavor
	postgresFlavor = sqlstore.PostgresFlavor
)

func PostgresFlavor() int {
	return sqlstore.PostgresFlavor
}

// SQLFlavor exposes the normalized flavor for callers that need an explicit check.
func SQLFlavor() int {
	return sqlstore.Flavor()
}

// SetSQLFlavorForTests sets the active SQL-flavor binder.
//
// This is intentionally narrow-scoped and used mainly by startup paths that
// want to force placeholder rewriting behavior without constructing a full core.
func SetSQLFlavorForTests(flavor int) {
	setSQLFlavor(flavor)
	projections.SetSQLFlavor(flavor)
}

func setSQLFlavor(flavor int) {
	sqlstore.SetFlavor(flavor)
}

type sqlLike = sqlstore.SQLLike

func qExec(execable sqlLike, query string, args ...any) (sql.Result, error) {
	return sqlstore.Exec(execable, query, args...)
}

func qQuery(queryable sqlLike, query string, args ...any) (*sql.Rows, error) {
	return sqlstore.Query(queryable, query, args...)
}

func qQueryRow(queryable sqlstore.RowQueryable, query string, args ...any) *sql.Row {
	return sqlstore.QueryRow(queryable, query, args...)
}

func queryPlaceholders(n int) string {
	return sqlstore.QueryPlaceholders(n)
}

func stringQueryArgs(values []string) []any {
	return sqlstore.StringQueryArgs(values)
}

func execReturningSeq(tx *sql.Tx, query string, args ...any) (int64, error) {
	query = rebindPlaceholders(query)
	if SQLFlavor() == postgresFlavor {
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
	return sqlstore.RebindPlaceholders(query)
}
