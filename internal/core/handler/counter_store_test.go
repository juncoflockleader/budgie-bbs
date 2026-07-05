package handler

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
	_ "modernc.org/sqlite"
)

func TestReactPostUsesCounterStore(t *testing.T) {
	db := openCounterStoreHandlerDB(t)
	store := &fakeCounterStore{reactionCount: 7}
	bus := &captureBus{}
	setCounterStoreRuntime(t, store)

	h := New(db, bus)
	reply := h.reactPost(&projections.User{ID: "bob-id", Name: "bob"}, proto.ReactPostPayload{Post: "post-1", Emoji: "heart"})
	if reply.Err != nil {
		t.Fatalf("reactPost error = %+v", reply.Err)
	}
	if want := []string{"userReacted:post-1:bob-id", "begin", "upsertReaction:post-1:bob-id:heart:1234", "reactionCount:post-1", "recordReactionReceived:alice-id", "commit"}; !stringSlicesEqual(store.calls, want) {
		t.Fatalf("counter store calls = %v, want %v", store.calls, want)
	}
	if len(bus.events) != 1 || bus.events[0].Kind != proto.EvtPostReacted {
		t.Fatalf("published events = %+v, want post reacted event", bus.events)
	}
	payload, ok := bus.events[0].Payload.(*proto.PostReactedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want *proto.PostReactedPayload", bus.events[0].Payload)
	}
	if payload.ReactionCount != 7 {
		t.Fatalf("reaction count = %d, want 7", payload.ReactionCount)
	}
}

func TestUnreactPostUsesCounterStore(t *testing.T) {
	db := openCounterStoreHandlerDB(t)
	store := &fakeCounterStore{reacted: true, reactionCount: 2}
	bus := &captureBus{}
	setCounterStoreRuntime(t, store)

	h := New(db, bus)
	reply := h.unreactPost(&projections.User{ID: "bob-id", Name: "bob"}, proto.ReactPostPayload{Post: "post-1"})
	if reply.Err != nil {
		t.Fatalf("unreactPost error = %+v", reply.Err)
	}
	if want := []string{"userReacted:post-1:bob-id", "begin", "deleteReaction:post-1:bob-id", "reactionCount:post-1", "recordReactionRemoved:alice-id", "commit"}; !stringSlicesEqual(store.calls, want) {
		t.Fatalf("counter store calls = %v, want %v", store.calls, want)
	}
	if len(bus.events) != 1 || bus.events[0].Kind != proto.EvtPostUnreacted {
		t.Fatalf("published events = %+v, want post unreacted event", bus.events)
	}
}

func TestVotePollUsesCounterStore(t *testing.T) {
	db := openCounterStoreHandlerDB(t)
	store := &fakeCounterStore{}
	bus := &captureBus{}
	setCounterStoreRuntime(t, store)

	h := New(db, bus)
	reply := h.votePoll(&projections.User{ID: "bob-id", Name: "bob"}, proto.VotePollPayload{Poll: "poll-1", Option: "option-1"})
	if reply.Err != nil {
		t.Fatalf("votePoll error = %+v", reply.Err)
	}
	if want := []string{"begin", "castVote:poll-1:option-1:bob-id:1234", "commit"}; !stringSlicesEqual(store.calls, want) {
		t.Fatalf("counter store calls = %v, want %v", store.calls, want)
	}
	if len(bus.events) != 1 || bus.events[0].Kind != proto.EvtPollVoted {
		t.Fatalf("published events = %+v, want poll voted event", bus.events)
	}
}

func TestReactPostRollsBackCounterMutationOnFailure(t *testing.T) {
	db := openCounterStoreHandlerDB(t)
	store := &fakeCounterStore{reactionCountErr: errors.New("count failed")}
	bus := &captureBus{}
	setCounterStoreRuntime(t, store)

	h := New(db, bus)
	reply := h.reactPost(&projections.User{ID: "bob-id", Name: "bob"}, proto.ReactPostPayload{Post: "post-1", Emoji: "heart"})
	if reply.Err == nil {
		t.Fatalf("reactPost error = nil, want counter failure")
	}
	if want := []string{"userReacted:post-1:bob-id", "begin", "upsertReaction:post-1:bob-id:heart:1234", "reactionCount:post-1", "rollback"}; !stringSlicesEqual(store.calls, want) {
		t.Fatalf("counter store calls = %v, want %v", store.calls, want)
	}
	if len(bus.events) != 0 {
		t.Fatalf("published events = %+v, want none after rollback", bus.events)
	}
}

func openCounterStoreHandlerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return db
}

func setCounterStoreRuntime(t *testing.T, store CounterStore) {
	t.Helper()
	SetRuntime(Runtime{
		CheckProcessed: func(_ *sql.DB, _, _, _, _, _ string) (string, bool, bool) {
			return "", false, false
		},
		NowMS: func() int64 {
			return 1234
		},
		GetPost: func(_ *sql.DB, id string) (*projections.Post, error) {
			if id != "post-1" {
				t.Fatalf("GetPost id = %s, want post-1", id)
			}
			return &projections.Post{ID: "post-1", Thread: "thread-1", Author: "alice", AuthorID: "alice-id"}, nil
		},
		GetThread: func(_ *sql.DB, id string) (*projections.Thread, error) {
			if id != "thread-1" {
				t.Fatalf("GetThread id = %s, want thread-1", id)
			}
			return &projections.Thread{ID: "thread-1", Board: "general"}, nil
		},
		GetPollWithVotes: func(_ *sql.DB, pollID, viewerUserID string) (*projections.Poll, error) {
			if pollID != "poll-1" {
				t.Fatalf("GetPollWithVotes poll = %s, want poll-1", pollID)
			}
			if viewerUserID != "bob-id" {
				t.Fatalf("GetPollWithVotes viewer = %s, want bob-id", viewerUserID)
			}
			return &projections.Poll{
				ID:     "poll-1",
				PostID: "post-1",
				Options: []projections.PollOption{
					{ID: "option-1", Text: "Option 1"},
				},
			}, nil
		},
		CounterStore: store,
	})
	t.Cleanup(func() {
		SetRuntime(Runtime{})
	})
}

type fakeCounterStore struct {
	reacted          bool
	reactionCount    int
	reactionCountErr error
	calls            []string
	mutationOpen     bool
}

func (s *fakeCounterStore) UserReacted(postID, userID string) (bool, error) {
	s.calls = append(s.calls, "userReacted:"+postID+":"+userID)
	return s.reacted, nil
}

func (s *fakeCounterStore) ReactionCount(postID string) (int, error) {
	s.calls = append(s.calls, "storeReactionCount:"+postID)
	return s.reactionCount, s.reactionCountErr
}

func (s *fakeCounterStore) PollOptionVoteCount(pollID, optionID string) (int, error) {
	s.calls = append(s.calls, "storePollOptionVoteCount:"+pollID+":"+optionID)
	return 0, nil
}

func (s *fakeCounterStore) PollVote(pollID, userID string) (string, bool, error) {
	s.calls = append(s.calls, "storePollVote:"+pollID+":"+userID)
	return "", false, nil
}

func (s *fakeCounterStore) UserCounterIdentity(userID string) (CounterUserIdentity, error) {
	s.calls = append(s.calls, "userCounterIdentity:"+userID)
	return CounterUserIdentity{}, nil
}

func (s *fakeCounterStore) BeginMutation() (CounterMutation, error) {
	s.calls = append(s.calls, "begin")
	s.mutationOpen = true
	return &fakeCounterMutation{store: s}, nil
}

type fakeCounterMutation struct {
	store  *fakeCounterStore
	closed bool
}

func (m *fakeCounterMutation) UpsertReaction(postID, userID, emoji string, ts int64) error {
	m.store.calls = append(m.store.calls, "upsertReaction:"+postID+":"+userID+":"+emoji+":"+strconv.FormatInt(ts, 10))
	return nil
}

func (m *fakeCounterMutation) DeleteReaction(postID, userID string) error {
	m.store.calls = append(m.store.calls, "deleteReaction:"+postID+":"+userID)
	return nil
}

func (m *fakeCounterMutation) ReactionCount(postID string) (int, error) {
	m.store.calls = append(m.store.calls, "reactionCount:"+postID)
	if m.store.reactionCountErr != nil {
		return 0, m.store.reactionCountErr
	}
	return m.store.reactionCount, nil
}

func (m *fakeCounterMutation) CastVote(pollID, optionID, userID string, ts int64) error {
	m.store.calls = append(m.store.calls, "castVote:"+pollID+":"+optionID+":"+userID+":"+strconv.FormatInt(ts, 10))
	return nil
}

func (m *fakeCounterMutation) DeletePollVote(pollID, userID string) error {
	m.store.calls = append(m.store.calls, "deletePollVote:"+pollID+":"+userID)
	return nil
}

func (m *fakeCounterMutation) RecordReactionReceived(postAuthorID string) error {
	m.store.calls = append(m.store.calls, "recordReactionReceived:"+postAuthorID)
	return nil
}

func (m *fakeCounterMutation) RecordReactionRemoved(postAuthorID string) error {
	m.store.calls = append(m.store.calls, "recordReactionRemoved:"+postAuthorID)
	return nil
}

func (m *fakeCounterMutation) ClearReactionReceived(userID string) error {
	m.store.calls = append(m.store.calls, "clearReactionReceived:"+userID)
	return nil
}

func (m *fakeCounterMutation) Commit() error {
	if m.closed {
		return nil
	}
	m.closed = true
	m.store.mutationOpen = false
	m.store.calls = append(m.store.calls, "commit")
	return nil
}

func (m *fakeCounterMutation) Rollback() error {
	if m.closed || !m.store.mutationOpen {
		return nil
	}
	m.closed = true
	m.store.mutationOpen = false
	m.store.calls = append(m.store.calls, "rollback")
	return nil
}

type captureBus struct {
	events []*proto.Event
}

func (b *captureBus) Publish(evt *proto.Event) {
	b.events = append(b.events, evt)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
