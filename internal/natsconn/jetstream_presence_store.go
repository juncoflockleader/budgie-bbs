package natsconn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	nats "github.com/nats-io/nats.go"
)

const (
	defaultJetStreamPresenceStoreBucket = "BUDGIE_PRESENCE_STORE"
	defaultJetStreamPresenceStoreTTL    = 15 * time.Minute
	jetStreamPresenceStoreRecordVersion = 1
	jetStreamPresenceStoreCASRetries    = 8
	jetStreamPresenceOnlineWindow       = 5 * time.Minute
	jetStreamPresenceCoalesceWindow     = 30 * time.Second
	jetStreamPresenceSessionKeyPrefix   = "presence_session/"
	jetStreamGuestPresenceKeyPrefix     = "guest_presence_session/"
)

type JetStreamPresenceStoreOptions struct {
	Bucket   string
	Replicas int
	Wait     time.Duration
	TTL      time.Duration
	ReadOnly bool
}

type JetStreamPresenceStore struct {
	kv presenceStoreKV
	db *sql.DB
}

type presenceStoreKV interface {
	Get(key string) (counterStoreKVEntry, error)
	Create(key string, value []byte) (uint64, error)
	Update(key string, value []byte, revision uint64) (uint64, error)
	Delete(key string, revision uint64) error
	Keys() ([]string, error)
}

type jetStreamPresenceSessionRecord struct {
	Version       int    `json:"v"`
	UserID        string `json:"userId"`
	SessionID     string `json:"sessionId"`
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	BoardID       string `json:"boardId"`
	ThreadID      string `json:"threadId"`
	LocationLabel string `json:"locationLabel"`
	FromHost      string `json:"fromHost"`
	LastSeen      int64  `json:"lastSeen"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type jetStreamGuestPresenceSessionRecord struct {
	Version       int    `json:"v"`
	SessionID     string `json:"sessionId"`
	Status        string `json:"status"`
	LocationLabel string `json:"locationLabel"`
	FromHost      string `json:"fromHost"`
	LastSeen      int64  `json:"lastSeen"`
	UpdatedAt     int64  `json:"updatedAt"`
}

var _ core.PresenceStore = (*JetStreamPresenceStore)(nil)

func NewJetStreamPresenceStore(ctx context.Context, conn *Conn, options JetStreamPresenceStoreOptions) (*JetStreamPresenceStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats presence store: nil connection")
	}
	bucket := strings.TrimSpace(options.Bucket)
	if bucket == "" {
		bucket = defaultJetStreamPresenceStoreBucket
	}
	wait := options.Wait
	if wait <= 0 {
		wait = defaultJetStreamEventLogWait
	}
	replicas := options.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultJetStreamPresenceStoreTTL
	}
	js, err := conn.nc.JetStream(nats.MaxWait(wait))
	if err != nil {
		return nil, err
	}
	kv, err := js.KeyValue(bucket)
	if errors.Is(err, nats.ErrBucketNotFound) && !options.ReadOnly {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:   bucket,
			History:  1,
			TTL:      ttl,
			Replicas: replicas,
			Storage:  nats.FileStorage,
		})
	}
	if err != nil {
		return nil, err
	}
	return newJetStreamPresenceStoreWithKV(natsCounterStoreKV{kv: kv}), nil
}

func newJetStreamPresenceStoreWithKV(kv presenceStoreKV) *JetStreamPresenceStore {
	return &JetStreamPresenceStore{kv: kv}
}

func (s *JetStreamPresenceStore) BindPresenceStoreDB(db *sql.DB) {
	if s == nil {
		return
	}
	s.db = db
}

func (s *JetStreamPresenceStore) SetUserPresence(userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	if err := s.requireStore(); err != nil {
		return err
	}
	record, err := normalizeJetStreamPresenceSessionRecord(jetStreamPresenceSessionRecord{
		UserID:        userID,
		SessionID:     sessionID,
		Status:        status,
		Mode:          mode,
		BoardID:       boardID,
		ThreadID:      threadID,
		LocationLabel: locationLabel,
		FromHost:      fromHost,
		LastSeen:      ts,
		UpdatedAt:     ts,
	})
	if err != nil {
		return err
	}
	key := jetStreamPresenceSessionKey(record.UserID, record.SessionID)
	data, err := encodeJetStreamPresenceSessionRecord(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamPresenceStoreCASRetries; attempt++ {
		previous, revision, found, err := s.readPresenceSessionRecord(key)
		if err != nil {
			return err
		}
		if found && jetStreamPresenceCanCoalesce(previous, record) {
			return nil
		}
		if !found {
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamPresenceStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamPresenceStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats presence store: session CAS failed after %d attempts", jetStreamPresenceStoreCASRetries)
}

func (s *JetStreamPresenceStore) SetGuestPresence(sessionID, status, locationLabel, fromHost string, ts int64) error {
	if err := s.requireStore(); err != nil {
		return err
	}
	record, err := normalizeJetStreamGuestPresenceSessionRecord(jetStreamGuestPresenceSessionRecord{
		SessionID:     sessionID,
		Status:        status,
		LocationLabel: locationLabel,
		FromHost:      fromHost,
		LastSeen:      ts,
		UpdatedAt:     ts,
	})
	if err != nil {
		return err
	}
	key := jetStreamGuestPresenceSessionKey(record.SessionID)
	data, err := encodeJetStreamGuestPresenceSessionRecord(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamPresenceStoreCASRetries; attempt++ {
		previous, revision, found, err := s.readGuestPresenceSessionRecord(key)
		if err != nil {
			return err
		}
		wasOnline := found && jetStreamGuestPresenceStatusCountsOnline(previous.Status)
		isOnline := jetStreamGuestPresenceStatusCountsOnline(record.Status)
		if found && isOnline && jetStreamGuestPresenceCanCoalesce(previous, record) {
			return nil
		}
		nextRevision := uint64(0)
		if !found {
			nextRevision, err = s.kv.Create(key, data)
			if err != nil {
				if isJetStreamPresenceStoreCASConflict(err) {
					continue
				}
				return err
			}
		} else {
			nextRevision, err = s.kv.Update(key, data, revision)
			if err != nil {
				if isJetStreamPresenceStoreCASConflict(err) {
					continue
				}
				return err
			}
		}
		if err := s.recordGuestPresenceTransition(wasOnline, isOnline, record.LastSeen); err != nil {
			if found {
				_ = s.restoreGuestPresenceSessionRecord(key, previous)
			} else {
				_ = s.kv.Delete(key, nextRevision)
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats presence store: guest session CAS failed after %d attempts", jetStreamPresenceStoreCASRetries)
}

func (s *JetStreamPresenceStore) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]core.SocialUser, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	boardID = strings.TrimSpace(boardID)
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return []core.SocialUser{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-jetStreamPresenceOnlineWindow).UnixMilli()
	viewerCanSeeCloak := s.viewerCanSeeCloak(viewerID)
	sessions := make([]jetStreamPresenceSessionRecord, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, jetStreamPresenceSessionKeyPrefix) {
			continue
		}
		record, _, found, err := s.readPresenceSessionRecord(key)
		if err != nil {
			return nil, err
		}
		if !found || !jetStreamPresenceRecordVisible(record, boardID, cutoff, viewerCanSeeCloak) {
			continue
		}
		sessions = append(sessions, record)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LastSeen != sessions[j].LastSeen {
			return sessions[i].LastSeen > sessions[j].LastSeen
		}
		if sessions[i].UserID != sessions[j].UserID {
			return sessions[i].UserID < sessions[j].UserID
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	if offset >= len(sessions) {
		return []core.SocialUser{}, nil
	}
	end := offset + limit
	if end > len(sessions) {
		end = len(sessions)
	}
	now := time.Now().UTC().UnixMilli()
	out := make([]core.SocialUser, 0, end-offset)
	for _, session := range sessions[offset:end] {
		user := core.SocialUser{
			UserID:        session.UserID,
			Name:          session.UserID,
			DisplayName:   session.UserID,
			SessionID:     session.SessionID,
			Kind:          "online",
			UpdatedAt:     session.UpdatedAt,
			Status:        session.Status,
			LastSeen:      session.LastSeen,
			Mode:          session.Mode,
			BoardID:       session.BoardID,
			ThreadID:      session.ThreadID,
			LocationLabel: session.LocationLabel,
			FromHost:      session.FromHost,
			Online:        true,
		}
		if user.LastSeen > 0 {
			user.IdleSeconds = (now - user.LastSeen) / 1000
		}
		keep, err := s.decorateOnlineUser(viewerID, &user)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, user)
		}
	}
	return out, nil
}

func (s *JetStreamPresenceStore) ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]core.SocialUser, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		roomID = "lobby"
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	sessions, err := s.chatSessions(viewerID, roomID)
	if err != nil {
		return nil, err
	}
	if offset >= len(sessions) {
		return []core.SocialUser{}, nil
	}
	end := offset + limit
	if end > len(sessions) {
		end = len(sessions)
	}
	now := time.Now().UTC().UnixMilli()
	out := make([]core.SocialUser, 0, end-offset)
	for _, session := range sessions[offset:end] {
		user := core.SocialUser{
			UserID:        session.UserID,
			Name:          session.UserID,
			DisplayName:   session.UserID,
			SessionID:     session.SessionID,
			Kind:          "chat",
			UpdatedAt:     session.UpdatedAt,
			Status:        session.Status,
			LastSeen:      session.LastSeen,
			Mode:          session.Mode,
			BoardID:       session.BoardID,
			ThreadID:      session.ThreadID,
			LocationLabel: session.LocationLabel,
			FromHost:      session.FromHost,
			Online:        true,
		}
		if user.LastSeen > 0 {
			user.IdleSeconds = (now - user.LastSeen) / 1000
		}
		keep, err := s.decorateOnlineUser(viewerID, &user)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, user)
		}
	}
	return out, nil
}

func (s *JetStreamPresenceStore) ChatOnlineCounts() (map[string]int, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-jetStreamPresenceOnlineWindow).UnixMilli()
	byRoom := map[string]map[string]bool{}
	for _, key := range keys {
		if !strings.HasPrefix(key, jetStreamPresenceSessionKeyPrefix) {
			continue
		}
		record, _, found, err := s.readPresenceSessionRecord(key)
		if err != nil {
			return nil, err
		}
		if !found ||
			record.LastSeen < cutoff ||
			strings.ToLower(strings.TrimSpace(record.Mode)) != "chat" ||
			strings.TrimSpace(record.LocationLabel) == "" ||
			!jetStreamPresenceStatusCountsOnline(record.Status) {
			continue
		}
		users := byRoom[record.LocationLabel]
		if users == nil {
			users = map[string]bool{}
			byRoom[record.LocationLabel] = users
		}
		users[record.UserID] = true
	}
	counts := map[string]int{}
	for roomID, users := range byRoom {
		counts[roomID] = len(users)
	}
	return counts, nil
}

func (s *JetStreamPresenceStore) chatSessions(viewerID, roomID string) ([]jetStreamPresenceSessionRecord, error) {
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return []jetStreamPresenceSessionRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-jetStreamPresenceOnlineWindow).UnixMilli()
	viewerCanSeeCloak := s.viewerCanSeeCloak(viewerID)
	sessions := make([]jetStreamPresenceSessionRecord, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, jetStreamPresenceSessionKeyPrefix) {
			continue
		}
		record, _, found, err := s.readPresenceSessionRecord(key)
		if err != nil {
			return nil, err
		}
		if !found ||
			record.LastSeen < cutoff ||
			strings.ToLower(strings.TrimSpace(record.Mode)) != "chat" ||
			record.LocationLabel != roomID {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(record.Status))
		if status == "" || status == "offline" || status == "invisible" {
			continue
		}
		if (status == "cloak" || status == "cloaked") && !viewerCanSeeCloak {
			continue
		}
		sessions = append(sessions, record)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LastSeen != sessions[j].LastSeen {
			return sessions[i].LastSeen > sessions[j].LastSeen
		}
		if sessions[i].UserID != sessions[j].UserID {
			return sessions[i].UserID < sessions[j].UserID
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions, nil
}

func (s *JetStreamPresenceStore) Stats() (core.PresenceStats, error) {
	if err := s.requireStore(); err != nil {
		return core.PresenceStats{}, err
	}
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		keys = nil
	} else if err != nil {
		return core.PresenceStats{}, err
	}
	cutoff := time.Now().UTC().Add(-jetStreamPresenceOnlineWindow).UnixMilli()
	onlineUsers := map[string]bool{}
	onlineGuests := 0
	for _, key := range keys {
		switch {
		case strings.HasPrefix(key, jetStreamPresenceSessionKeyPrefix):
			record, _, found, err := s.readPresenceSessionRecord(key)
			if err != nil {
				return core.PresenceStats{}, err
			}
			if found && record.LastSeen >= cutoff && jetStreamPresenceStatusCountsOnline(record.Status) {
				onlineUsers[record.UserID] = true
			}
		case strings.HasPrefix(key, jetStreamGuestPresenceKeyPrefix):
			record, _, found, err := s.readGuestPresenceSessionRecord(key)
			if err != nil {
				return core.PresenceStats{}, err
			}
			if found && record.LastSeen >= cutoff && jetStreamGuestPresenceStatusCountsOnline(record.Status) {
				onlineGuests++
			}
		}
	}
	logins, err := s.presenceCount(jetStreamGuestPresenceTotalLoginsKey())
	if err != nil {
		return core.PresenceStats{}, err
	}
	logouts, err := s.presenceCount(jetStreamGuestPresenceTotalLogoutsKey())
	if err != nil {
		return core.PresenceStats{}, err
	}
	return core.PresenceStats{
		OnlineUsers:       len(onlineUsers),
		OnlineGuests:      onlineGuests,
		TotalGuestLogins:  logins,
		TotalGuestLogouts: logouts,
	}, nil
}

func (s *JetStreamPresenceStore) requireStore() error {
	if s == nil || s.kv == nil {
		return sql.ErrConnDone
	}
	return nil
}

func (s *JetStreamPresenceStore) readPresenceSessionRecord(key string) (jetStreamPresenceSessionRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamPresenceStoreKeyNotFound(err) {
		return jetStreamPresenceSessionRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamPresenceSessionRecord{}, 0, false, err
	}
	record, err := decodeJetStreamPresenceSessionRecord(entry.Value())
	if err != nil {
		return jetStreamPresenceSessionRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamPresenceStore) readGuestPresenceSessionRecord(key string) (jetStreamGuestPresenceSessionRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamPresenceStoreKeyNotFound(err) {
		return jetStreamGuestPresenceSessionRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamGuestPresenceSessionRecord{}, 0, false, err
	}
	record, err := decodeJetStreamGuestPresenceSessionRecord(entry.Value())
	if err != nil {
		return jetStreamGuestPresenceSessionRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamPresenceStore) readPresenceCountRecord(key string) (jetStreamCounterCountRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamPresenceStoreKeyNotFound(err) {
		return jetStreamCounterCountRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamCounterCountRecord{}, 0, false, err
	}
	record, err := decodeJetStreamCounterCountRecord(entry.Value())
	if err != nil {
		return jetStreamCounterCountRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamPresenceStore) presenceCount(key string) (int, error) {
	record, _, found, err := s.readPresenceCountRecord(key)
	if err != nil || !found {
		return 0, err
	}
	return record.Count, nil
}

func (s *JetStreamPresenceStore) recordGuestPresenceTransition(wasOnline, isOnline bool, ts int64) error {
	switch {
	case !wasOnline && isOnline:
		return s.incrementPresenceCount(jetStreamGuestPresenceTotalLoginsKey(), 1, ts)
	case wasOnline && !isOnline:
		return s.incrementPresenceCount(jetStreamGuestPresenceTotalLogoutsKey(), 1, ts)
	default:
		return nil
	}
}

func (s *JetStreamPresenceStore) incrementPresenceCount(key string, delta int, ts int64) error {
	if delta == 0 {
		return nil
	}
	for attempt := 0; attempt < jetStreamPresenceStoreCASRetries; attempt++ {
		record, revision, found, err := s.readPresenceCountRecord(key)
		if err != nil {
			return err
		}
		if !found {
			if delta <= 0 {
				return nil
			}
			data, err := encodeJetStreamCounterCountRecord(jetStreamCounterCountRecord{
				Count:     delta,
				UpdatedAt: ts,
			})
			if err != nil {
				return err
			}
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamPresenceStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		next := record.Count + delta
		if next < 0 {
			next = 0
		}
		record.Count = next
		record.UpdatedAt = ts
		data, err := encodeJetStreamCounterCountRecord(record)
		if err != nil {
			return err
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamPresenceStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats presence store: count CAS failed after %d attempts", jetStreamPresenceStoreCASRetries)
}

func (s *JetStreamPresenceStore) restoreGuestPresenceSessionRecord(key string, record jetStreamGuestPresenceSessionRecord) error {
	data, err := encodeJetStreamGuestPresenceSessionRecord(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamPresenceStoreCASRetries; attempt++ {
		_, revision, found, err := s.readGuestPresenceSessionRecord(key)
		if err != nil {
			return err
		}
		if !found {
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamPresenceStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamPresenceStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats presence store: guest restore CAS failed after %d attempts", jetStreamPresenceStoreCASRetries)
}

func (s *JetStreamPresenceStore) viewerCanSeeCloak(viewerID string) bool {
	if s == nil || s.db == nil || strings.TrimSpace(viewerID) == "" {
		return false
	}
	var role string
	if err := s.queryRow(`SELECT role FROM users WHERE id=?`, viewerID).Scan(&role); err != nil {
		return false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "moderator" || role == "admin"
}

func (s *JetStreamPresenceStore) decorateOnlineUser(viewerID string, user *core.SocialUser) (bool, error) {
	if s == nil || s.db == nil || user == nil {
		return true, nil
	}
	err := s.queryRow(
		`SELECT u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name)
		   FROM users u
		   LEFT JOIN user_profiles up ON up.user_id = u.id
		  WHERE u.id=?`,
		user.UserID,
	).Scan(&user.Name, &user.Role, &user.DisplayName)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if user.BoardID != "" {
		err = s.queryRow(`SELECT name FROM boards WHERE id=?`, user.BoardID).Scan(&user.BoardName)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
	}
	if strings.TrimSpace(viewerID) == "" {
		return true, nil
	}
	var mutual, ignored int
	err = s.queryRow(
		`SELECT
		     CASE WHEN EXISTS (
		       SELECT 1 FROM user_relationships mine
		        WHERE mine.user_id = ? AND mine.target_user_id = ? AND mine.kind='friend'
		     ) AND EXISTS (
		       SELECT 1 FROM user_relationships back
		        WHERE back.user_id = ? AND back.target_user_id = ? AND back.kind='friend'
		     ) THEN 1 ELSE 0 END,
		     CASE WHEN EXISTS (
		       SELECT 1 FROM user_relationships ig
		        WHERE ig.user_id = ? AND ig.target_user_id = ? AND ig.kind='ignore'
		     ) THEN 1 ELSE 0 END`,
		viewerID,
		user.UserID,
		user.UserID,
		viewerID,
		viewerID,
		user.UserID,
	).Scan(&mutual, &ignored)
	if err != nil {
		return false, err
	}
	user.Mutual = mutual != 0
	user.Ignored = ignored != 0
	return true, nil
}

func (s *JetStreamPresenceStore) queryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(projections.RebindPlaceholders(query), args...)
}

func normalizeJetStreamPresenceSessionRecord(record jetStreamPresenceSessionRecord) (jetStreamPresenceSessionRecord, error) {
	record.UserID = strings.TrimSpace(record.UserID)
	if record.UserID == "" {
		return jetStreamPresenceSessionRecord{}, fmt.Errorf("nats presence store: user id required")
	}
	record.SessionID = strings.TrimSpace(record.SessionID)
	if record.SessionID == "" {
		record.SessionID = "default"
	}
	record.Status = strings.TrimSpace(record.Status)
	record.Mode = strings.TrimSpace(record.Mode)
	record.BoardID = strings.TrimSpace(record.BoardID)
	record.ThreadID = strings.TrimSpace(record.ThreadID)
	record.LocationLabel = strings.TrimSpace(record.LocationLabel)
	record.FromHost = strings.TrimSpace(record.FromHost)
	if record.LastSeen <= 0 {
		record.LastSeen = time.Now().UTC().UnixMilli()
	}
	if record.UpdatedAt <= 0 {
		record.UpdatedAt = record.LastSeen
	}
	return record, nil
}

func normalizeJetStreamGuestPresenceSessionRecord(record jetStreamGuestPresenceSessionRecord) (jetStreamGuestPresenceSessionRecord, error) {
	record.SessionID = strings.TrimSpace(record.SessionID)
	if record.SessionID == "" {
		return jetStreamGuestPresenceSessionRecord{}, fmt.Errorf("nats presence store: guest session id required")
	}
	if len(record.SessionID) > 120 {
		record.SessionID = record.SessionID[:120]
	}
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = "active"
	}
	if len(record.Status) > 40 {
		record.Status = record.Status[:40]
	}
	record.LocationLabel = strings.TrimSpace(record.LocationLabel)
	if len(record.LocationLabel) > 120 {
		record.LocationLabel = record.LocationLabel[:120]
	}
	record.FromHost = strings.TrimSpace(record.FromHost)
	if len(record.FromHost) > 120 {
		record.FromHost = record.FromHost[:120]
	}
	if record.LastSeen <= 0 {
		record.LastSeen = time.Now().UTC().UnixMilli()
	}
	if record.UpdatedAt <= 0 {
		record.UpdatedAt = record.LastSeen
	}
	return record, nil
}

func encodeJetStreamPresenceSessionRecord(record jetStreamPresenceSessionRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamPresenceStoreRecordVersion
	}
	if record.Version != jetStreamPresenceStoreRecordVersion {
		return nil, fmt.Errorf("nats presence store: unsupported session version %d", record.Version)
	}
	if strings.TrimSpace(record.UserID) == "" || strings.TrimSpace(record.SessionID) == "" {
		return nil, fmt.Errorf("nats presence store: session record missing identity")
	}
	return json.Marshal(record)
}

func encodeJetStreamGuestPresenceSessionRecord(record jetStreamGuestPresenceSessionRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamPresenceStoreRecordVersion
	}
	if record.Version != jetStreamPresenceStoreRecordVersion {
		return nil, fmt.Errorf("nats presence store: unsupported guest session version %d", record.Version)
	}
	if strings.TrimSpace(record.SessionID) == "" {
		return nil, fmt.Errorf("nats presence store: guest session record missing identity")
	}
	return json.Marshal(record)
}

func decodeJetStreamPresenceSessionRecord(data []byte) (jetStreamPresenceSessionRecord, error) {
	var record jetStreamPresenceSessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamPresenceSessionRecord{}, err
	}
	if record.Version != jetStreamPresenceStoreRecordVersion {
		return jetStreamPresenceSessionRecord{}, fmt.Errorf("nats presence store: unsupported session version %d", record.Version)
	}
	if strings.TrimSpace(record.UserID) == "" || strings.TrimSpace(record.SessionID) == "" {
		return jetStreamPresenceSessionRecord{}, fmt.Errorf("nats presence store: session record missing identity")
	}
	return record, nil
}

func decodeJetStreamGuestPresenceSessionRecord(data []byte) (jetStreamGuestPresenceSessionRecord, error) {
	var record jetStreamGuestPresenceSessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamGuestPresenceSessionRecord{}, err
	}
	if record.Version != jetStreamPresenceStoreRecordVersion {
		return jetStreamGuestPresenceSessionRecord{}, fmt.Errorf("nats presence store: unsupported guest session version %d", record.Version)
	}
	if strings.TrimSpace(record.SessionID) == "" {
		return jetStreamGuestPresenceSessionRecord{}, fmt.Errorf("nats presence store: guest session record missing identity")
	}
	return record, nil
}

func jetStreamPresenceRecordVisible(record jetStreamPresenceSessionRecord, boardID string, cutoff int64, viewerCanSeeCloak bool) bool {
	if record.LastSeen < cutoff {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(record.Status))
	if status == "" || status == "offline" || status == "invisible" {
		return false
	}
	if (status == "cloak" || status == "cloaked") && !viewerCanSeeCloak {
		return false
	}
	return boardID == "" || record.BoardID == boardID
}

func jetStreamGuestPresenceCanCoalesce(previous, next jetStreamGuestPresenceSessionRecord) bool {
	return previous.Status == next.Status &&
		previous.LocationLabel == next.LocationLabel &&
		previous.FromHost == next.FromHost &&
		previous.LastSeen > 0 &&
		(next.LastSeen <= previous.LastSeen || next.LastSeen-previous.LastSeen < jetStreamPresenceCoalesceWindow.Milliseconds())
}

func jetStreamPresenceCanCoalesce(previous, next jetStreamPresenceSessionRecord) bool {
	return jetStreamPresenceStatusCountsOnline(next.Status) &&
		previous.Status == next.Status &&
		previous.Mode == next.Mode &&
		previous.BoardID == next.BoardID &&
		previous.ThreadID == next.ThreadID &&
		previous.LocationLabel == next.LocationLabel &&
		previous.FromHost == next.FromHost &&
		previous.LastSeen > 0 &&
		(next.LastSeen <= previous.LastSeen || next.LastSeen-previous.LastSeen < jetStreamPresenceCoalesceWindow.Milliseconds())
}

func jetStreamGuestPresenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "inactive":
		return false
	default:
		return true
	}
}

func jetStreamPresenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "invisible", "cloak", "cloaked":
		return false
	default:
		return true
	}
}

func jetStreamPresenceSessionKey(userID, sessionID string) string {
	return jetStreamPresenceSessionKeyPrefix + jetStreamCounterKeyPart(userID) + "/" + jetStreamCounterKeyPart(sessionID)
}

func jetStreamGuestPresenceSessionKey(sessionID string) string {
	return jetStreamGuestPresenceKeyPrefix + jetStreamCounterKeyPart(sessionID)
}

func jetStreamGuestPresenceTotalLoginsKey() string {
	return "guest_presence_count/logins"
}

func jetStreamGuestPresenceTotalLogoutsKey() string {
	return "guest_presence_count/logouts"
}

func isJetStreamPresenceStoreKeyNotFound(err error) bool {
	return errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted)
}

func isJetStreamPresenceStoreCASConflict(err error) bool {
	return errors.Is(err, nats.ErrKeyExists) || isWrongLastSequence(err)
}
