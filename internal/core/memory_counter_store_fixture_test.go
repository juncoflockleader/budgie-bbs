package core

import (
	"database/sql"
	"sync"

	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
)

const defaultMemoryCounterStoreShards = 64

// MemoryCounterStore is a test-only non-SQL CounterStore fixture. It keeps
// identity rows and aggregate counts in identity-sharded memory maps so
// command handlers can exercise the same backend-neutral contract as the
// production backends (sql and nats-kv, see -counter-store).
type MemoryCounterStore struct {
	mu     sync.Mutex
	shards []memoryCounterShard
}

type memoryCounterShard struct {
	reactions            map[memoryReactionKey]memoryReaction
	postReactionCounts   map[string]int
	pollVotes            map[memoryPollVoteKey]string
	pollOptionVoteCounts map[memoryPollOptionKey]int
	reactionsReceived    map[string]int
}

type memoryReactionKey struct {
	postID string
	userID string
}

type memoryReaction struct {
	emoji string
	ts    int64
}

type memoryPollVoteKey struct {
	pollID string
	userID string
}

type memoryPollOptionKey struct {
	pollID   string
	optionID string
}

func NewMemoryCounterStore(shards int) *MemoryCounterStore {
	if shards <= 0 {
		shards = defaultMemoryCounterStoreShards
	}
	store := &MemoryCounterStore{
		shards: make([]memoryCounterShard, shards),
	}
	for i := range store.shards {
		store.shards[i] = memoryCounterShard{
			reactions:            map[memoryReactionKey]memoryReaction{},
			postReactionCounts:   map[string]int{},
			pollVotes:            map[memoryPollVoteKey]string{},
			pollOptionVoteCounts: map[memoryPollOptionKey]int{},
			reactionsReceived:    map[string]int{},
		}
	}
	return store
}

func (s *MemoryCounterStore) UserReacted(postID, userID string) (bool, error) {
	if s == nil {
		return false, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	shard := s.shardForIdentity(userID)
	_, ok := shard.reactions[memoryReactionKey{postID: postID, userID: userID}]
	return ok, nil
}

func (s *MemoryCounterStore) ReactionCount(postID string) (int, error) {
	if s == nil {
		return 0, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reactionCountLocked(postID), nil
}

func (s *MemoryCounterStore) PollOptionVoteCount(pollID, optionID string) (int, error) {
	if s == nil {
		return 0, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pollOptionVoteCountLocked(pollID, optionID), nil
}

func (s *MemoryCounterStore) PollVote(pollID, userID string) (string, bool, error) {
	if s == nil {
		return "", false, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	shard := s.shardForIdentity(userID)
	optionID, ok := shard.pollVotes[memoryPollVoteKey{pollID: pollID, userID: userID}]
	return optionID, ok, nil
}

func (s *MemoryCounterStore) UserCounterIdentity(userID string) (CounterUserIdentity, error) {
	if s == nil {
		return CounterUserIdentity{}, sql.ErrConnDone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := CounterUserIdentity{}
	shard := s.shardForIdentity(userID)
	for key, reaction := range shard.reactions {
		if key.userID != userID {
			continue
		}
		identity.Reactions = append(identity.Reactions, CounterReactionIdentity{
			PostID: key.postID,
			UserID: key.userID,
			Emoji:  reaction.emoji,
			TS:     reaction.ts,
		})
	}
	for key, optionID := range shard.pollVotes {
		if key.userID != userID {
			continue
		}
		identity.PollVotes = append(identity.PollVotes, CounterPollVoteIdentity{
			PollID:   key.pollID,
			OptionID: optionID,
			UserID:   key.userID,
		})
	}
	return identity, nil
}

func (s *MemoryCounterStore) ReactionReceivedCount(userID string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shardForIdentity(userID).reactionsReceived[userID]
}

func (s *MemoryCounterStore) BeginMutation() (CounterMutation, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	s.mu.Lock()
	return &memoryCounterMutation{store: s}, nil
}

func (s *MemoryCounterStore) shardForIdentity(identity string) *memoryCounterShard {
	shard := counterstore.ShardForIdentity(identity)
	if len(s.shards) > 0 {
		shard %= len(s.shards)
	}
	return &s.shards[shard]
}

func (s *MemoryCounterStore) reactionCountLocked(postID string) int {
	total := 0
	for i := range s.shards {
		total += s.shards[i].postReactionCounts[postID]
	}
	return total
}

func (s *MemoryCounterStore) pollOptionVoteCountLocked(pollID, optionID string) int {
	total := 0
	key := memoryPollOptionKey{pollID: pollID, optionID: optionID}
	for i := range s.shards {
		total += s.shards[i].pollOptionVoteCounts[key]
	}
	return total
}

type memoryCounterMutation struct {
	store  *MemoryCounterStore
	undo   []func()
	closed bool
}

func (m *memoryCounterMutation) UpsertReaction(postID, userID, emoji string, ts int64) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(userID)
	key := memoryReactionKey{postID: postID, userID: userID}
	previous, existed := shard.reactions[key]
	shard.reactions[key] = memoryReaction{emoji: emoji, ts: ts}
	if existed {
		m.undo = append(m.undo, func() {
			shard.reactions[key] = previous
		})
		return nil
	}
	shard.postReactionCounts[postID]++
	m.undo = append(m.undo, func() {
		delete(shard.reactions, key)
		incrementMemoryCount(shard.postReactionCounts, postID, -1)
	})
	return nil
}

func (m *memoryCounterMutation) DeleteReaction(postID, userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(userID)
	key := memoryReactionKey{postID: postID, userID: userID}
	previous, existed := shard.reactions[key]
	if !existed {
		return nil
	}
	delete(shard.reactions, key)
	incrementMemoryCount(shard.postReactionCounts, postID, -1)
	m.undo = append(m.undo, func() {
		shard.reactions[key] = previous
		incrementMemoryCount(shard.postReactionCounts, postID, 1)
	})
	return nil
}

func (m *memoryCounterMutation) ReactionCount(postID string) (int, error) {
	if err := m.requireOpen(); err != nil {
		return 0, err
	}
	return m.store.reactionCountLocked(postID), nil
}

func (m *memoryCounterMutation) CastVote(pollID, optionID, userID string, ts int64) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(userID)
	voteKey := memoryPollVoteKey{pollID: pollID, userID: userID}
	previousOption, existed := shard.pollVotes[voteKey]
	if existed && previousOption == optionID {
		return nil
	}
	if existed {
		incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: previousOption}, -1)
	}
	shard.pollVotes[voteKey] = optionID
	incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: optionID}, 1)
	m.undo = append(m.undo, func() {
		incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: optionID}, -1)
		if existed {
			shard.pollVotes[voteKey] = previousOption
			incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: previousOption}, 1)
		} else {
			delete(shard.pollVotes, voteKey)
		}
	})
	return nil
}

func (m *memoryCounterMutation) DeletePollVote(pollID, userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(userID)
	voteKey := memoryPollVoteKey{pollID: pollID, userID: userID}
	previousOption, existed := shard.pollVotes[voteKey]
	if !existed {
		return nil
	}
	delete(shard.pollVotes, voteKey)
	incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: previousOption}, -1)
	m.undo = append(m.undo, func() {
		shard.pollVotes[voteKey] = previousOption
		incrementMemoryCount(shard.pollOptionVoteCounts, memoryPollOptionKey{pollID: pollID, optionID: previousOption}, 1)
	})
	return nil
}

func (m *memoryCounterMutation) RecordReactionReceived(postAuthorID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(postAuthorID)
	incrementMemoryCount(shard.reactionsReceived, postAuthorID, 1)
	m.undo = append(m.undo, func() {
		incrementMemoryCount(shard.reactionsReceived, postAuthorID, -1)
	})
	return nil
}

func (m *memoryCounterMutation) RecordReactionRemoved(postAuthorID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(postAuthorID)
	previous := shard.reactionsReceived[postAuthorID]
	if previous > 0 {
		incrementMemoryCount(shard.reactionsReceived, postAuthorID, -1)
	}
	m.undo = append(m.undo, func() {
		if previous == 0 {
			delete(shard.reactionsReceived, postAuthorID)
			return
		}
		shard.reactionsReceived[postAuthorID] = previous
	})
	return nil
}

func (m *memoryCounterMutation) ClearReactionReceived(userID string) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	shard := m.store.shardForIdentity(userID)
	previous, existed := shard.reactionsReceived[userID]
	delete(shard.reactionsReceived, userID)
	m.undo = append(m.undo, func() {
		if existed {
			shard.reactionsReceived[userID] = previous
		}
	})
	return nil
}

func (m *memoryCounterMutation) Commit() error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	m.closed = true
	m.undo = nil
	m.store.mu.Unlock()
	return nil
}

func (m *memoryCounterMutation) Rollback() error {
	if m == nil || m.closed {
		return nil
	}
	for i := len(m.undo) - 1; i >= 0; i-- {
		m.undo[i]()
	}
	m.closed = true
	m.undo = nil
	m.store.mu.Unlock()
	return nil
}

func (m *memoryCounterMutation) requireOpen() error {
	if m == nil || m.store == nil || m.closed {
		return sql.ErrTxDone
	}
	return nil
}

func incrementMemoryCount[K comparable](counts map[K]int, key K, delta int) {
	next := counts[key] + delta
	if next <= 0 {
		delete(counts, key)
		return
	}
	counts[key] = next
}
