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
	nats "github.com/nats-io/nats.go"
)

const (
	defaultJetStreamChatStoreBucket = "BUDGIE_CHAT_STORE"
	jetStreamChatStoreRecordVersion = 1
	jetStreamChatStoreHistoryLimit  = 200
	jetStreamChatRoomKeyPrefix      = "chat_room/"
	jetStreamChatLineKeyPrefix      = "chat_line/"
)

type JetStreamChatStoreOptions struct {
	Bucket   string
	Replicas int
	Wait     time.Duration
	ReadOnly bool
}

type JetStreamChatStore struct {
	kv chatStoreKV
}

type chatStoreKV interface {
	Get(key string) (counterStoreKVEntry, error)
	Create(key string, value []byte) (uint64, error)
	Update(key string, value []byte, revision uint64) (uint64, error)
	Delete(key string, revision uint64) error
	Keys() ([]string, error)
}

type jetStreamChatRoomRecord struct {
	Version   int    `json:"v"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Topic     string `json:"topic,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type jetStreamChatLineRecord struct {
	Version   int    `json:"v"`
	ID        string `json:"id"`
	RoomID    string `json:"roomId"`
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

var _ core.ChatStore = (*JetStreamChatStore)(nil)

func NewJetStreamChatStore(ctx context.Context, conn *Conn, options JetStreamChatStoreOptions) (*JetStreamChatStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats chat store: nil connection")
	}
	bucket := JetStreamName(options.Bucket, defaultJetStreamChatStoreBucket)
	wait := jetStreamWait(options.Wait)
	replicas := jetStreamReplicas(options.Replicas)
	js, err := conn.nc.JetStream(nats.MaxWait(wait))
	if err != nil {
		return nil, err
	}
	kv, err := js.KeyValue(bucket)
	if errors.Is(err, nats.ErrBucketNotFound) && !options.ReadOnly {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:   bucket,
			History:  1,
			Replicas: replicas,
			Storage:  nats.FileStorage,
		})
	}
	if err != nil {
		return nil, err
	}
	return newJetStreamChatStoreWithKV(natsCounterStoreKV{kv: kv}), nil
}

func newJetStreamChatStoreWithKV(kv chatStoreKV) *JetStreamChatStore {
	return &JetStreamChatStore{kv: kv}
}

func (s *JetStreamChatStore) InsertChatLine(id, roomID, roomName, userID, userName, body string, ts int64) error {
	if err := s.requireStore(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	roomID = strings.TrimSpace(roomID)
	roomName = strings.TrimSpace(roomName)
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	body = strings.TrimSpace(body)
	if id == "" || roomID == "" || userID == "" || body == "" {
		return fmt.Errorf("chat line id, room, user, and body are required")
	}
	if roomName == "" {
		roomName = roomID
	}
	if ts <= 0 {
		ts = time.Now().UnixMilli()
	}

	room := jetStreamChatRoomRecord{
		ID:        roomID,
		Name:      roomName,
		CreatedBy: userID,
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	if err := s.upsertRoom(room); err != nil {
		return err
	}
	line := jetStreamChatLineRecord{
		ID:        id,
		RoomID:    roomID,
		UserID:    userID,
		UserName:  userName,
		Body:      body,
		CreatedAt: ts,
	}
	lineData, err := encodeJetStreamChatLineRecord(line)
	if err != nil {
		return err
	}
	if _, err := s.kv.Create(jetStreamChatLineKey(roomID, ts, id), lineData); err != nil {
		return fmt.Errorf("nats chat store: create line: %w", err)
	}
	return s.trimRoom(roomID)
}

func (s *JetStreamChatStore) ListChatRooms() ([]core.ChatRoom, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	keys, err := s.keys()
	if err != nil {
		return nil, err
	}
	rooms := []core.ChatRoom{}
	seenLobby := false
	for _, key := range keys {
		if !strings.HasPrefix(key, jetStreamChatRoomKeyPrefix) {
			continue
		}
		record, _, found, err := s.readRoomRecord(key)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		room := chatRoomRecordToCore(record)
		room.LineCount, err = s.roomLineCount(room.ID)
		if err != nil {
			return nil, err
		}
		if room.ID == "lobby" {
			seenLobby = true
		}
		rooms = append(rooms, room)
	}
	if !seenLobby {
		rooms = append(rooms, core.ChatRoom{
			ID:   "lobby",
			Name: "Lobby",
		})
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		if rooms[i].ID == "lobby" || rooms[j].ID == "lobby" {
			return rooms[i].ID == "lobby"
		}
		if rooms[i].UpdatedAt != rooms[j].UpdatedAt {
			return rooms[i].UpdatedAt > rooms[j].UpdatedAt
		}
		return rooms[i].Name < rooms[j].Name
	})
	return rooms, nil
}

func (s *JetStreamChatStore) ListChatLines(roomID string, limit int) ([]core.ChatLine, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		roomID = "lobby"
	}
	if limit <= 0 || limit > jetStreamChatStoreHistoryLimit {
		limit = 50
	}
	lines, err := s.roomLines(roomID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt > lines[j].CreatedAt
		}
		return lines[i].ID > lines[j].ID
	})
	if limit < len(lines) {
		lines = lines[:limit]
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CreatedAt != lines[j].CreatedAt {
			return lines[i].CreatedAt < lines[j].CreatedAt
		}
		return lines[i].ID < lines[j].ID
	})
	return lines, nil
}

func (s *JetStreamChatStore) upsertRoom(next jetStreamChatRoomRecord) error {
	key := jetStreamChatRoomKey(next.ID)
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		current, revision, found, err := s.readRoomRecord(key)
		if err != nil {
			return err
		}
		if found {
			if current.Name == "" {
				current.Name = next.Name
			}
			if current.CreatedBy == "" {
				current.CreatedBy = next.CreatedBy
			}
			if current.CreatedAt <= 0 {
				current.CreatedAt = next.CreatedAt
			}
			if next.UpdatedAt > current.UpdatedAt {
				current.UpdatedAt = next.UpdatedAt
			}
			data, err := encodeJetStreamChatRoomRecord(current)
			if err != nil {
				return err
			}
			if _, err := s.kv.Update(key, data, revision); err != nil {
				if isJetStreamChatStoreCASConflict(err) {
					continue
				}
				return fmt.Errorf("nats chat store: update room: %w", err)
			}
			return nil
		}
		data, err := encodeJetStreamChatRoomRecord(next)
		if err != nil {
			return err
		}
		if _, err := s.kv.Create(key, data); err != nil {
			if isJetStreamChatStoreCASConflict(err) {
				continue
			}
			return fmt.Errorf("nats chat store: create room: %w", err)
		}
		return nil
	}
	return fmt.Errorf("nats chat store: room CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (s *JetStreamChatStore) trimRoom(roomID string) error {
	lines, err := s.roomLineRecords(roomID)
	if err != nil {
		return err
	}
	if len(lines) <= jetStreamChatStoreHistoryLimit {
		return nil
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].record.CreatedAt != lines[j].record.CreatedAt {
			return lines[i].record.CreatedAt > lines[j].record.CreatedAt
		}
		return lines[i].record.ID > lines[j].record.ID
	})
	for _, line := range lines[jetStreamChatStoreHistoryLimit:] {
		if err := s.kv.Delete(line.key, line.revision); err != nil && !isJetStreamChatStoreKeyNotFound(err) {
			return fmt.Errorf("nats chat store: trim line: %w", err)
		}
	}
	return nil
}

func (s *JetStreamChatStore) roomLineCount(roomID string) (int, error) {
	lines, err := s.roomLineRecords(roomID)
	if err != nil {
		return 0, err
	}
	return len(lines), nil
}

func (s *JetStreamChatStore) roomLines(roomID string) ([]core.ChatLine, error) {
	records, err := s.roomLineRecords(roomID)
	if err != nil {
		return nil, err
	}
	lines := make([]core.ChatLine, 0, len(records))
	for _, record := range records {
		lines = append(lines, chatLineRecordToCore(record.record))
	}
	return lines, nil
}

type jetStreamChatLineRecordWithKey struct {
	key      string
	revision uint64
	record   jetStreamChatLineRecord
}

func (s *JetStreamChatStore) roomLineRecords(roomID string) ([]jetStreamChatLineRecordWithKey, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		roomID = "lobby"
	}
	keys, err := s.keys()
	if err != nil {
		return nil, err
	}
	prefix := jetStreamChatLineRoomPrefix(roomID)
	out := []jetStreamChatLineRecordWithKey{}
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		record, revision, found, err := s.readLineRecord(key)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, jetStreamChatLineRecordWithKey{
				key:      key,
				revision: revision,
				record:   record,
			})
		}
	}
	return out, nil
}

func (s *JetStreamChatStore) keys() ([]string, error) {
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *JetStreamChatStore) readRoomRecord(key string) (jetStreamChatRoomRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamChatStoreKeyNotFound(err) {
		return jetStreamChatRoomRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamChatRoomRecord{}, 0, false, err
	}
	record, err := decodeJetStreamChatRoomRecord(entry.Value())
	if err != nil {
		return jetStreamChatRoomRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamChatStore) readLineRecord(key string) (jetStreamChatLineRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamChatStoreKeyNotFound(err) {
		return jetStreamChatLineRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamChatLineRecord{}, 0, false, err
	}
	record, err := decodeJetStreamChatLineRecord(entry.Value())
	if err != nil {
		return jetStreamChatLineRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamChatStore) requireStore() error {
	if s == nil || s.kv == nil {
		return sql.ErrConnDone
	}
	return nil
}

func encodeJetStreamChatRoomRecord(record jetStreamChatRoomRecord) ([]byte, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.Name = strings.TrimSpace(record.Name)
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	if record.ID == "" {
		return nil, fmt.Errorf("nats chat store: room id is required")
	}
	if record.Name == "" {
		record.Name = record.ID
	}
	if record.UpdatedAt <= 0 {
		record.UpdatedAt = record.CreatedAt
	}
	record.Version = jetStreamChatStoreRecordVersion
	return json.Marshal(record)
}

func decodeJetStreamChatRoomRecord(data []byte) (jetStreamChatRoomRecord, error) {
	var record jetStreamChatRoomRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamChatRoomRecord{}, err
	}
	if record.Version != jetStreamChatStoreRecordVersion {
		return jetStreamChatRoomRecord{}, fmt.Errorf("nats chat store: unsupported room version %d", record.Version)
	}
	record.ID = strings.TrimSpace(record.ID)
	record.Name = strings.TrimSpace(record.Name)
	if record.ID == "" {
		return jetStreamChatRoomRecord{}, fmt.Errorf("nats chat store: room id is required")
	}
	if record.Name == "" {
		record.Name = record.ID
	}
	return record, nil
}

func encodeJetStreamChatLineRecord(record jetStreamChatLineRecord) ([]byte, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.RoomID = strings.TrimSpace(record.RoomID)
	record.UserID = strings.TrimSpace(record.UserID)
	record.UserName = strings.TrimSpace(record.UserName)
	record.Body = strings.TrimSpace(record.Body)
	if record.ID == "" || record.RoomID == "" || record.UserID == "" || record.Body == "" {
		return nil, fmt.Errorf("nats chat store: line id, room, user, and body are required")
	}
	if record.CreatedAt <= 0 {
		record.CreatedAt = time.Now().UnixMilli()
	}
	record.Version = jetStreamChatStoreRecordVersion
	return json.Marshal(record)
}

func decodeJetStreamChatLineRecord(data []byte) (jetStreamChatLineRecord, error) {
	var record jetStreamChatLineRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamChatLineRecord{}, err
	}
	if record.Version != jetStreamChatStoreRecordVersion {
		return jetStreamChatLineRecord{}, fmt.Errorf("nats chat store: unsupported line version %d", record.Version)
	}
	record.ID = strings.TrimSpace(record.ID)
	record.RoomID = strings.TrimSpace(record.RoomID)
	record.UserID = strings.TrimSpace(record.UserID)
	record.Body = strings.TrimSpace(record.Body)
	if record.ID == "" || record.RoomID == "" || record.UserID == "" || record.Body == "" {
		return jetStreamChatLineRecord{}, fmt.Errorf("nats chat store: line id, room, user, and body are required")
	}
	return record, nil
}

func chatRoomRecordToCore(record jetStreamChatRoomRecord) core.ChatRoom {
	return core.ChatRoom{
		ID:        record.ID,
		Name:      record.Name,
		Topic:     record.Topic,
		CreatedBy: record.CreatedBy,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

func chatLineRecordToCore(record jetStreamChatLineRecord) core.ChatLine {
	return core.ChatLine{
		ID:        record.ID,
		Room:      record.RoomID,
		UserID:    record.UserID,
		User:      record.UserName,
		Text:      record.Body,
		CreatedAt: record.CreatedAt,
		TS:        record.CreatedAt,
	}
}

func jetStreamChatRoomKey(roomID string) string {
	return jetStreamChatRoomKeyPrefix + jetStreamCounterKeyPart(roomID)
}

func jetStreamChatLineRoomPrefix(roomID string) string {
	return jetStreamChatLineKeyPrefix + jetStreamCounterKeyPart(roomID) + "/"
}

func jetStreamChatLineKey(roomID string, ts int64, lineID string) string {
	return fmt.Sprintf("%s%020d/%s", jetStreamChatLineRoomPrefix(roomID), ts, jetStreamCounterKeyPart(lineID))
}

func isJetStreamChatStoreKeyNotFound(err error) bool {
	return errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted)
}

func isJetStreamChatStoreCASConflict(err error) bool {
	return errors.Is(err, nats.ErrKeyExists) || isWrongLastSequence(err)
}
