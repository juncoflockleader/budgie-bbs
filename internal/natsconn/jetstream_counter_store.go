package natsconn

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	nats "github.com/nats-io/nats.go"
)

const (
	defaultJetStreamCounterStoreBucket = "BUDGIE_COUNTER_STORE"
	defaultJetStreamCounterStoreShards = 64
	jetStreamCounterStoreRecordVersion = 1
	jetStreamCounterStoreCASRetries    = 8
	jetStreamReactionKeyPrefix         = "reaction/"
	jetStreamReactionCountKeyPrefix    = "reaction_count/"
	jetStreamPollVoteKeyPrefix         = "poll_vote/"
	jetStreamPollOptionCountKeyPrefix  = "poll_option_vote_count/"
)

type JetStreamCounterStoreOptions struct {
	Bucket   string
	Shards   int
	Replicas int
	Wait     time.Duration
	ReadOnly bool
}

type JetStreamCounterStore struct {
	kv     counterStoreKV
	shards int
}

type counterStoreKV interface {
	Get(key string) (counterStoreKVEntry, error)
	Create(key string, value []byte) (uint64, error)
	Update(key string, value []byte, revision uint64) (uint64, error)
	Delete(key string, revision uint64) error
	Keys() ([]string, error)
}

type counterStoreKVEntry interface {
	Value() []byte
	Revision() uint64
}

type natsCounterStoreKV struct {
	kv nats.KeyValue
}

func (k natsCounterStoreKV) Get(key string) (counterStoreKVEntry, error) {
	return k.kv.Get(key)
}

func (k natsCounterStoreKV) Create(key string, value []byte) (uint64, error) {
	return k.kv.Create(key, value)
}

func (k natsCounterStoreKV) Update(key string, value []byte, revision uint64) (uint64, error) {
	return k.kv.Update(key, value, revision)
}

func (k natsCounterStoreKV) Delete(key string, revision uint64) error {
	if revision == 0 {
		return k.kv.Delete(key)
	}
	return k.kv.Delete(key, nats.LastRevision(revision))
}

func (k natsCounterStoreKV) Keys() ([]string, error) {
	return k.kv.Keys()
}

type jetStreamCounterReactionRecord struct {
	Version int    `json:"v"`
	PostID  string `json:"postId"`
	UserID  string `json:"userId"`
	Emoji   string `json:"emoji"`
	TS      int64  `json:"ts"`
}

type jetStreamCounterPollVoteRecord struct {
	Version  int    `json:"v"`
	PollID   string `json:"pollId"`
	OptionID string `json:"optionId"`
	UserID   string `json:"userId"`
	TS       int64  `json:"ts"`
}

type jetStreamCounterCountRecord struct {
	Version   int   `json:"v"`
	Count     int   `json:"count"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

type JetStreamCounterStoreRepairResult struct {
	ReactionIdentityRecords int
	ReactionShardRecords    int
	PollVoteIdentityRecords int
	PollVoteShardRecords    int
	DeletedShardRecords     int
}

var _ core.CounterStore = (*JetStreamCounterStore)(nil)

func NewJetStreamCounterStore(ctx context.Context, conn *Conn, options JetStreamCounterStoreOptions) (*JetStreamCounterStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conn == nil || conn.nc == nil {
		return nil, fmt.Errorf("nats counter store: nil connection")
	}
	bucket := JetStreamName(options.Bucket, defaultJetStreamCounterStoreBucket)
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
			History:  16,
			Replicas: replicas,
			Storage:  nats.FileStorage,
		})
	}
	if err != nil {
		return nil, err
	}
	return newJetStreamCounterStoreWithKV(natsCounterStoreKV{kv: kv}, options.Shards), nil
}

func newJetStreamCounterStoreWithKV(kv counterStoreKV, shards int) *JetStreamCounterStore {
	if shards <= 0 {
		shards = defaultJetStreamCounterStoreShards
	}
	return &JetStreamCounterStore{
		kv:     kv,
		shards: shards,
	}
}

func (s *JetStreamCounterStore) UserReacted(postID, userID string) (bool, error) {
	if err := s.requireStore(); err != nil {
		return false, err
	}
	_, _, found, err := s.readReactionRecord(jetStreamReactionKey(postID, userID))
	return found, err
}

func (s *JetStreamCounterStore) ReactionCount(postID string) (int, error) {
	if err := s.requireStore(); err != nil {
		return 0, err
	}
	total := 0
	for shard := 0; shard < s.shards; shard++ {
		record, _, found, err := s.readCountRecord(jetStreamReactionCountKey(postID, shard))
		if err != nil {
			return 0, err
		}
		if found {
			total += record.Count
		}
	}
	return total, nil
}

func (s *JetStreamCounterStore) PollOptionVoteCount(pollID, optionID string) (int, error) {
	if err := s.requireStore(); err != nil {
		return 0, err
	}
	total := 0
	for shard := 0; shard < s.shards; shard++ {
		record, _, found, err := s.readCountRecord(jetStreamPollOptionVoteCountKey(pollID, optionID, shard))
		if err != nil {
			return 0, err
		}
		if found {
			total += record.Count
		}
	}
	return total, nil
}

func (s *JetStreamCounterStore) PollVote(pollID, userID string) (string, bool, error) {
	if err := s.requireStore(); err != nil {
		return "", false, err
	}
	record, _, found, err := s.readPollVoteRecord(jetStreamPollVoteKey(pollID, userID))
	if err != nil || !found {
		return "", found, err
	}
	return record.OptionID, true, nil
}

func (s *JetStreamCounterStore) UserCounterIdentity(userID string) (core.CounterUserIdentity, error) {
	if err := s.requireStore(); err != nil {
		return core.CounterUserIdentity{}, err
	}
	keys, err := s.keys()
	if err != nil {
		return core.CounterUserIdentity{}, err
	}
	identity := core.CounterUserIdentity{}
	encodedUserSuffix := "/" + jetStreamCounterKeyPart(userID)
	for _, key := range keys {
		switch {
		case strings.HasPrefix(key, jetStreamReactionKeyPrefix) && strings.HasSuffix(key, encodedUserSuffix):
			record, _, found, err := s.readReactionRecord(key)
			if err != nil {
				return identity, fmt.Errorf("nats counter store: read user reaction identity %s: %w", key, err)
			}
			if found && record.UserID == userID {
				identity.Reactions = append(identity.Reactions, core.CounterReactionIdentity{
					PostID: record.PostID,
					UserID: record.UserID,
					Emoji:  record.Emoji,
					TS:     record.TS,
				})
			}
		case strings.HasPrefix(key, jetStreamPollVoteKeyPrefix) && strings.HasSuffix(key, encodedUserSuffix):
			record, _, found, err := s.readPollVoteRecord(key)
			if err != nil {
				return identity, fmt.Errorf("nats counter store: read user poll vote identity %s: %w", key, err)
			}
			if found && record.UserID == userID {
				identity.PollVotes = append(identity.PollVotes, core.CounterPollVoteIdentity{
					PollID:   record.PollID,
					OptionID: record.OptionID,
					UserID:   record.UserID,
					TS:       record.TS,
				})
			}
		}
	}
	return identity, nil
}

func (s *JetStreamCounterStore) ReactionReceivedCount(userID string) (int, error) {
	if err := s.requireStore(); err != nil {
		return 0, err
	}
	record, _, found, err := s.readCountRecord(jetStreamReactionReceivedCountKey(userID, s.shardForIdentity(userID)))
	if err != nil || !found {
		return 0, err
	}
	return record.Count, nil
}

func (s *JetStreamCounterStore) BeginMutation() (core.CounterMutation, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	return &jetStreamCounterMutation{store: s}, nil
}

func (s *JetStreamCounterStore) RebuildAggregateShardsFromIdentity(ts int64) (JetStreamCounterStoreRepairResult, error) {
	if err := s.requireStore(); err != nil {
		return JetStreamCounterStoreRepairResult{}, err
	}
	if ts <= 0 {
		ts = time.Now().UnixMilli()
	}
	keys, err := s.keys()
	if err != nil {
		return JetStreamCounterStoreRepairResult{}, err
	}
	result := JetStreamCounterStoreRepairResult{}
	reactionCounts := map[string]int{}
	pollCounts := map[string]int{}
	existingAggregates := map[string]bool{}
	for _, key := range keys {
		switch {
		case strings.HasPrefix(key, jetStreamReactionKeyPrefix):
			record, _, found, err := s.readReactionRecord(key)
			if err != nil {
				return result, fmt.Errorf("nats counter store: read reaction identity %s: %w", key, err)
			}
			if !found {
				continue
			}
			result.ReactionIdentityRecords++
			countKey := jetStreamReactionCountKey(record.PostID, s.shardForIdentity(record.UserID))
			reactionCounts[countKey]++
		case strings.HasPrefix(key, jetStreamPollVoteKeyPrefix):
			record, _, found, err := s.readPollVoteRecord(key)
			if err != nil {
				return result, fmt.Errorf("nats counter store: read poll vote identity %s: %w", key, err)
			}
			if !found {
				continue
			}
			result.PollVoteIdentityRecords++
			countKey := jetStreamPollOptionVoteCountKey(record.PollID, record.OptionID, s.shardForIdentity(record.UserID))
			pollCounts[countKey]++
		case strings.HasPrefix(key, jetStreamReactionCountKeyPrefix),
			strings.HasPrefix(key, jetStreamPollOptionCountKeyPrefix):
			existingAggregates[key] = true
		}
	}
	for key, count := range reactionCounts {
		if err := s.setCountRecord(key, count, ts); err != nil {
			return result, fmt.Errorf("nats counter store: rebuild reaction shard %s: %w", key, err)
		}
		result.ReactionShardRecords++
		delete(existingAggregates, key)
	}
	for key, count := range pollCounts {
		if err := s.setCountRecord(key, count, ts); err != nil {
			return result, fmt.Errorf("nats counter store: rebuild poll shard %s: %w", key, err)
		}
		result.PollVoteShardRecords++
		delete(existingAggregates, key)
	}
	for key := range existingAggregates {
		if err := s.deleteCountRecord(key); err != nil {
			return result, fmt.Errorf("nats counter store: delete stale shard %s: %w", key, err)
		}
		result.DeletedShardRecords++
	}
	return result, nil
}

func (s *JetStreamCounterStore) requireStore() error {
	if s == nil || s.kv == nil {
		return sql.ErrConnDone
	}
	if s.shards <= 0 {
		return fmt.Errorf("nats counter store: invalid shard count %d", s.shards)
	}
	return nil
}

func (s *JetStreamCounterStore) shardForIdentity(identity string) int {
	if s == nil || s.shards <= 0 || identity == "" {
		return 0
	}
	sum := 0
	for i := 0; i < len(identity); i++ {
		sum += int(identity[i])
	}
	return sum % s.shards
}

func (s *JetStreamCounterStore) readReactionRecord(key string) (jetStreamCounterReactionRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamCounterStoreKeyNotFound(err) {
		return jetStreamCounterReactionRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamCounterReactionRecord{}, 0, false, err
	}
	record, err := decodeJetStreamCounterReactionRecord(entry.Value())
	if err != nil {
		return jetStreamCounterReactionRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamCounterStore) readPollVoteRecord(key string) (jetStreamCounterPollVoteRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamCounterStoreKeyNotFound(err) {
		return jetStreamCounterPollVoteRecord{}, 0, false, nil
	}
	if err != nil {
		return jetStreamCounterPollVoteRecord{}, 0, false, err
	}
	record, err := decodeJetStreamCounterPollVoteRecord(entry.Value())
	if err != nil {
		return jetStreamCounterPollVoteRecord{}, 0, false, err
	}
	return record, entry.Revision(), true, nil
}

func (s *JetStreamCounterStore) readCountRecord(key string) (jetStreamCounterCountRecord, uint64, bool, error) {
	entry, err := s.kv.Get(key)
	if isJetStreamCounterStoreKeyNotFound(err) {
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

func (s *JetStreamCounterStore) keys() ([]string, error) {
	keys, err := s.kv.Keys()
	if errors.Is(err, nats.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *JetStreamCounterStore) setCountRecord(key string, count int, ts int64) error {
	if count <= 0 {
		return s.deleteCountRecord(key)
	}
	data, err := encodeJetStreamCounterCountRecord(jetStreamCounterCountRecord{
		Count:     count,
		UpdatedAt: ts,
	})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		_, revision, found, err := s.readCountRecord(key)
		if err != nil {
			return err
		}
		if !found {
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats counter store: set count CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (s *JetStreamCounterStore) deleteCountRecord(key string) error {
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		_, revision, found, err := s.readCountRecord(key)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := s.kv.Delete(key, revision); err != nil {
			if isJetStreamCounterStoreKeyNotFound(err) {
				return nil
			}
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats counter store: delete count CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (s *JetStreamCounterStore) incrementCount(key string, delta int, ts int64) error {
	if delta == 0 {
		return nil
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		record, revision, found, err := s.readCountRecord(key)
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
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}

		next := record.Count + delta
		if next <= 0 {
			if err := s.kv.Delete(key, revision); err != nil {
				if isJetStreamCounterStoreKeyNotFound(err) {
					return nil
				}
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		record.Count = next
		record.UpdatedAt = ts
		data, err := encodeJetStreamCounterCountRecord(record)
		if err != nil {
			return err
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats counter store: count CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (s *JetStreamCounterStore) restoreReactionRecord(key string, record jetStreamCounterReactionRecord) error {
	data, err := encodeJetStreamCounterReactionRecord(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		_, revision, found, err := s.readReactionRecord(key)
		if err != nil {
			return err
		}
		if !found {
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats counter store: reaction restore CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (s *JetStreamCounterStore) restorePollVoteRecord(key string, record jetStreamCounterPollVoteRecord) error {
	data, err := encodeJetStreamCounterPollVoteRecord(record)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		_, revision, found, err := s.readPollVoteRecord(key)
		if err != nil {
			return err
		}
		if !found {
			if _, err := s.kv.Create(key, data); err != nil {
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			return nil
		}
		if _, err := s.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("nats counter store: poll vote restore CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

type jetStreamCounterMutation struct {
	store  *JetStreamCounterStore
	undo   []func()
	closed bool
}

func (m *jetStreamCounterMutation) UpsertReaction(postID, userID, emoji string, ts int64) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	key := jetStreamReactionKey(postID, userID)
	countKey := jetStreamReactionCountKey(postID, m.store.shardForIdentity(userID))
	next := jetStreamCounterReactionRecord{PostID: postID, UserID: userID, Emoji: emoji, TS: ts}
	data, err := encodeJetStreamCounterReactionRecord(next)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		previous, revision, found, err := m.store.readReactionRecord(key)
		if err != nil {
			return err
		}
		if !found {
			newRevision, err := m.store.kv.Create(key, data)
			if err != nil {
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			if err := m.store.incrementCount(countKey, 1, ts); err != nil {
				_ = m.store.kv.Delete(key, newRevision)
				return err
			}
			m.undo = append(m.undo, func() {
				_ = m.store.kv.Delete(key, newRevision)
				_ = m.store.incrementCount(countKey, -1, ts)
			})
			return nil
		}
		if _, err := m.store.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		m.undo = append(m.undo, func() {
			_ = m.store.restoreReactionRecord(key, previous)
		})
		return nil
	}
	return fmt.Errorf("nats counter store: reaction upsert CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (m *jetStreamCounterMutation) DeleteReaction(postID, userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	key := jetStreamReactionKey(postID, userID)
	countKey := jetStreamReactionCountKey(postID, m.store.shardForIdentity(userID))
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		previous, revision, found, err := m.store.readReactionRecord(key)
		if err != nil || !found {
			return err
		}
		if err := m.store.kv.Delete(key, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		if err := m.store.incrementCount(countKey, -1, previous.TS); err != nil {
			_ = m.store.restoreReactionRecord(key, previous)
			return err
		}
		m.undo = append(m.undo, func() {
			_ = m.store.restoreReactionRecord(key, previous)
			_ = m.store.incrementCount(countKey, 1, previous.TS)
		})
		return nil
	}
	return fmt.Errorf("nats counter store: reaction delete CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (m *jetStreamCounterMutation) ReactionCount(postID string) (int, error) {
	if err := m.requireOpen(); err != nil {
		return 0, err
	}
	return m.store.ReactionCount(postID)
}

func (m *jetStreamCounterMutation) CastVote(pollID, optionID, userID string, ts int64) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	key := jetStreamPollVoteKey(pollID, userID)
	newCountKey := jetStreamPollOptionVoteCountKey(pollID, optionID, m.store.shardForIdentity(userID))
	next := jetStreamCounterPollVoteRecord{PollID: pollID, OptionID: optionID, UserID: userID, TS: ts}
	data, err := encodeJetStreamCounterPollVoteRecord(next)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		previous, revision, found, err := m.store.readPollVoteRecord(key)
		if err != nil {
			return err
		}
		if !found {
			newRevision, err := m.store.kv.Create(key, data)
			if err != nil {
				if isJetStreamCounterStoreCASConflict(err) {
					continue
				}
				return err
			}
			if err := m.store.incrementCount(newCountKey, 1, ts); err != nil {
				_ = m.store.kv.Delete(key, newRevision)
				return err
			}
			m.undo = append(m.undo, func() {
				_ = m.store.kv.Delete(key, newRevision)
				_ = m.store.incrementCount(newCountKey, -1, ts)
			})
			return nil
		}
		if _, err := m.store.kv.Update(key, data, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		if previous.OptionID == optionID {
			m.undo = append(m.undo, func() {
				_ = m.store.restorePollVoteRecord(key, previous)
			})
			return nil
		}
		oldCountKey := jetStreamPollOptionVoteCountKey(pollID, previous.OptionID, m.store.shardForIdentity(userID))
		if err := m.store.incrementCount(oldCountKey, -1, previous.TS); err != nil {
			_ = m.store.restorePollVoteRecord(key, previous)
			return err
		}
		if err := m.store.incrementCount(newCountKey, 1, ts); err != nil {
			_ = m.store.incrementCount(oldCountKey, 1, previous.TS)
			_ = m.store.restorePollVoteRecord(key, previous)
			return err
		}
		m.undo = append(m.undo, func() {
			_ = m.store.incrementCount(newCountKey, -1, ts)
			_ = m.store.incrementCount(oldCountKey, 1, previous.TS)
			_ = m.store.restorePollVoteRecord(key, previous)
		})
		return nil
	}
	return fmt.Errorf("nats counter store: poll vote CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (m *jetStreamCounterMutation) DeletePollVote(pollID, userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	key := jetStreamPollVoteKey(pollID, userID)
	for attempt := 0; attempt < jetStreamCounterStoreCASRetries; attempt++ {
		previous, revision, found, err := m.store.readPollVoteRecord(key)
		if err != nil || !found {
			return err
		}
		if err := m.store.kv.Delete(key, revision); err != nil {
			if isJetStreamCounterStoreCASConflict(err) {
				continue
			}
			return err
		}
		countKey := jetStreamPollOptionVoteCountKey(pollID, previous.OptionID, m.store.shardForIdentity(userID))
		if err := m.store.incrementCount(countKey, -1, previous.TS); err != nil {
			_ = m.store.restorePollVoteRecord(key, previous)
			return err
		}
		m.undo = append(m.undo, func() {
			_ = m.store.restorePollVoteRecord(key, previous)
			_ = m.store.incrementCount(countKey, 1, previous.TS)
		})
		return nil
	}
	return fmt.Errorf("nats counter store: poll vote delete CAS failed after %d attempts", jetStreamCounterStoreCASRetries)
}

func (m *jetStreamCounterMutation) RecordReactionReceived(postAuthorID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	ts := time.Now().UnixMilli()
	key := jetStreamReactionReceivedCountKey(postAuthorID, m.store.shardForIdentity(postAuthorID))
	if err := m.store.incrementCount(key, 1, ts); err != nil {
		return err
	}
	m.undo = append(m.undo, func() {
		_ = m.store.incrementCount(key, -1, ts)
	})
	return nil
}

func (m *jetStreamCounterMutation) RecordReactionRemoved(postAuthorID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	ts := time.Now().UnixMilli()
	key := jetStreamReactionReceivedCountKey(postAuthorID, m.store.shardForIdentity(postAuthorID))
	previous, _, found, err := m.store.readCountRecord(key)
	if err != nil {
		return err
	}
	if err := m.store.incrementCount(key, -1, ts); err != nil {
		return err
	}
	if found && previous.Count > 0 {
		m.undo = append(m.undo, func() {
			_ = m.store.incrementCount(key, 1, ts)
		})
	}
	return nil
}

func (m *jetStreamCounterMutation) ClearReactionReceived(userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	key := jetStreamReactionReceivedCountKey(userID, m.store.shardForIdentity(userID))
	previous, _, found, err := m.store.readCountRecord(key)
	if err != nil || !found {
		return err
	}
	if err := m.store.deleteCountRecord(key); err != nil {
		return err
	}
	m.undo = append(m.undo, func() {
		_ = m.store.setCountRecord(key, previous.Count, previous.UpdatedAt)
	})
	return nil
}

func (m *jetStreamCounterMutation) Commit() error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	m.closed = true
	m.undo = nil
	return nil
}

func (m *jetStreamCounterMutation) Rollback() error {
	if m == nil || m.closed {
		return nil
	}
	for i := len(m.undo) - 1; i >= 0; i-- {
		m.undo[i]()
	}
	m.closed = true
	m.undo = nil
	return nil
}

func (m *jetStreamCounterMutation) requireOpen() error {
	if m == nil || m.store == nil || m.closed {
		return sql.ErrTxDone
	}
	return m.store.requireStore()
}

func encodeJetStreamCounterReactionRecord(record jetStreamCounterReactionRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamCounterStoreRecordVersion
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return nil, fmt.Errorf("nats counter store: unsupported reaction version %d", record.Version)
	}
	if strings.TrimSpace(record.PostID) == "" || strings.TrimSpace(record.UserID) == "" {
		return nil, fmt.Errorf("nats counter store: reaction record missing identity")
	}
	return json.Marshal(record)
}

func decodeJetStreamCounterReactionRecord(data []byte) (jetStreamCounterReactionRecord, error) {
	var record jetStreamCounterReactionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamCounterReactionRecord{}, err
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return jetStreamCounterReactionRecord{}, fmt.Errorf("nats counter store: unsupported reaction version %d", record.Version)
	}
	if strings.TrimSpace(record.PostID) == "" || strings.TrimSpace(record.UserID) == "" {
		return jetStreamCounterReactionRecord{}, fmt.Errorf("nats counter store: reaction record missing identity")
	}
	return record, nil
}

func encodeJetStreamCounterPollVoteRecord(record jetStreamCounterPollVoteRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamCounterStoreRecordVersion
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return nil, fmt.Errorf("nats counter store: unsupported poll vote version %d", record.Version)
	}
	if strings.TrimSpace(record.PollID) == "" || strings.TrimSpace(record.OptionID) == "" || strings.TrimSpace(record.UserID) == "" {
		return nil, fmt.Errorf("nats counter store: poll vote record missing identity")
	}
	return json.Marshal(record)
}

func decodeJetStreamCounterPollVoteRecord(data []byte) (jetStreamCounterPollVoteRecord, error) {
	var record jetStreamCounterPollVoteRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamCounterPollVoteRecord{}, err
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return jetStreamCounterPollVoteRecord{}, fmt.Errorf("nats counter store: unsupported poll vote version %d", record.Version)
	}
	if strings.TrimSpace(record.PollID) == "" || strings.TrimSpace(record.OptionID) == "" || strings.TrimSpace(record.UserID) == "" {
		return jetStreamCounterPollVoteRecord{}, fmt.Errorf("nats counter store: poll vote record missing identity")
	}
	return record, nil
}

func encodeJetStreamCounterCountRecord(record jetStreamCounterCountRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = jetStreamCounterStoreRecordVersion
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return nil, fmt.Errorf("nats counter store: unsupported count version %d", record.Version)
	}
	if record.Count < 0 {
		return nil, fmt.Errorf("nats counter store: negative count %d", record.Count)
	}
	return json.Marshal(record)
}

func decodeJetStreamCounterCountRecord(data []byte) (jetStreamCounterCountRecord, error) {
	var record jetStreamCounterCountRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return jetStreamCounterCountRecord{}, err
	}
	if record.Version != jetStreamCounterStoreRecordVersion {
		return jetStreamCounterCountRecord{}, fmt.Errorf("nats counter store: unsupported count version %d", record.Version)
	}
	if record.Count < 0 {
		return jetStreamCounterCountRecord{}, fmt.Errorf("nats counter store: negative count %d", record.Count)
	}
	return record, nil
}

func jetStreamReactionKey(postID, userID string) string {
	return jetStreamReactionKeyPrefix + jetStreamCounterKeyPart(postID) + "/" + jetStreamCounterKeyPart(userID)
}

func jetStreamReactionCountKey(postID string, shard int) string {
	return fmt.Sprintf("%s%s/%d", jetStreamReactionCountKeyPrefix, jetStreamCounterKeyPart(postID), shard)
}

func jetStreamPollVoteKey(pollID, userID string) string {
	return jetStreamPollVoteKeyPrefix + jetStreamCounterKeyPart(pollID) + "/" + jetStreamCounterKeyPart(userID)
}

func jetStreamPollOptionVoteCountKey(pollID, optionID string, shard int) string {
	return fmt.Sprintf("%s%s/%s/%d", jetStreamPollOptionCountKeyPrefix, jetStreamCounterKeyPart(pollID), jetStreamCounterKeyPart(optionID), shard)
}

func jetStreamReactionReceivedCountKey(userID string, shard int) string {
	return fmt.Sprintf("reaction_received/%s/%d", jetStreamCounterKeyPart(userID), shard)
}

func jetStreamCounterKeyPart(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func isJetStreamCounterStoreKeyNotFound(err error) bool {
	return errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted)
}

func isJetStreamCounterStoreCASConflict(err error) bool {
	return errors.Is(err, nats.ErrKeyExists) || isWrongLastSequence(err)
}
