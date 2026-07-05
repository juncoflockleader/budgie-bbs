package projections

import (
	"database/sql"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

const (
	sqliteFlavor   = sqlstore.SQLiteFlavor
	postgresFlavor = sqlstore.PostgresFlavor
)

func SetSQLFlavor(flavor int) {
	sqlstore.SetFlavor(flavor)
}

func SQLFlavor() int {
	return sqlstore.Flavor()
}

type sqlLike = sqlstore.SQLLike

func QExec(execable sqlLike, query string, args ...any) (sql.Result, error) {
	return sqlstore.Exec(execable, query, args...)
}

func QQuery(queryable sqlLike, query string, args ...any) (*sql.Rows, error) {
	return sqlstore.Query(queryable, query, args...)
}

func QQueryRow(queryable sqlLike, query string, args ...any) *sql.Row {
	return sqlstore.QueryRow(queryable, query, args...)
}

func RebindPlaceholders(query string) string {
	return sqlstore.RebindPlaceholders(query)
}

func NowMS() int64 { return time.Now().UnixMilli() }
