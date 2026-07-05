package handler

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/chatstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/presencestore"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func nowMS() int64 {
	return currentRuntime().NowMS()
}

func newID(prefix string) string {
	return currentRuntime().NewID(prefix)
}

func checkProcessed(db *sql.DB, partitionKind, partitionKey, actorID, cid, commandHash string) (string, bool, bool) {
	return currentRuntime().CheckProcessed(db, partitionKind, partitionKey, actorID, cid, commandHash)
}

func qQueryRow(queryable interface {
	QueryRow(query string, args ...any) *sql.Row
}, query string, args ...any) *sql.Row {
	return currentRuntime().QQueryRow(queryable, query, args...)
}

func appendEvent(tx *sql.Tx, id string, kind proto.EventKind, scopes []string, payload any) (int64, error) {
	return currentRuntime().AppendEvent(tx, id, kind, scopes, payload)
}

func counterStore() counterstore.Store {
	store := currentRuntime().CounterStore
	if store == nil {
		panic("handler counter store not configured")
	}
	return store
}

func presenceStore() presencestore.Store {
	store := currentRuntime().PresenceStore
	if store == nil {
		panic("handler presence store not configured")
	}
	return store
}

func chatStore() chatstore.Store {
	store := currentRuntime().ChatStore
	if store == nil {
		panic("handler chat store not configured")
	}
	return store
}

func beginCounterMutation() (counterstore.Mutation, error) {
	return counterStore().BeginMutation()
}

func userReacted(postID, userID string) (bool, error) {
	return counterStore().UserReacted(postID, userID)
}

func setUserPresence(db *sql.DB, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	if currentRuntime().PresenceStore != nil {
		return presenceStore().SetUserPresence(userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts)
	}
	return currentRuntime().SetUserPresence(db, userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost, ts)
}

func pgNotifyEphemeral(db *sql.DB, event, eid, scopes string) {
	if fn := currentRuntime().PGNotifyEphemeral; fn != nil {
		fn(db, event, eid, scopes)
	}
}
