package ratelimit

import (
	"errors"
	"testing"
	"time"
)

// fakeStore is a controllable Store for exercising the Limiter integration
// without a real Redis.
type fakeStore struct {
	retry      map[string]time.Duration
	failReturn time.Duration
	err        error
	failCalls  []string
	resetCalls []string
}

func (f *fakeStore) Fail(key string, threshold int, window, lockout time.Duration) (time.Duration, error) {
	f.failCalls = append(f.failCalls, key)
	return f.failReturn, f.err
}
func (f *fakeStore) RetryAfter(key string) (time.Duration, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.retry[key], nil
}
func (f *fakeStore) Reset(key string) error {
	f.resetCalls = append(f.resetCalls, key)
	return f.err
}

func TestLimiterHonorsSharedStoreLockout(t *testing.T) {
	// High local threshold so the local limiter never locks on its own — the
	// lockout must come purely from the shared store (i.e. another node locked it).
	l := New(1000, time.Minute, time.Minute)
	st := &fakeStore{retry: map[string]time.Duration{"k": 90 * time.Second}, failReturn: 90 * time.Second}
	l.SetStore(st)

	if got := l.RetryAfter("k"); got <= 0 {
		t.Fatalf("expected the shared store's lockout to be honored, got %v", got)
	}
	if !l.Fail("k") {
		t.Fatal("expected Fail to report locked when the shared store locks the key")
	}
	if len(st.failCalls) == 0 {
		t.Fatal("expected the failure to be recorded in the shared store")
	}
}

func TestLimiterFailsOpenWhenStoreErrors(t *testing.T) {
	l := New(5, time.Minute, time.Minute) // under threshold locally
	l.SetStore(&fakeStore{err: errors.New("redis down")})

	// Store errors must not lock anyone out or panic; degrade to local-only.
	if got := l.RetryAfter("k"); got != 0 {
		t.Fatalf("store error should fail open (0), got %v", got)
	}
	if l.Fail("k") {
		t.Fatal("a single failure under the local threshold must not lock, even with a failing store")
	}
}

func TestLimiterResetPropagatesToStore(t *testing.T) {
	l := New(5, time.Minute, time.Minute)
	st := &fakeStore{}
	l.SetStore(st)
	l.Reset("k")
	if len(st.resetCalls) != 1 || st.resetCalls[0] != "k" {
		t.Fatalf("expected Reset to propagate to the store, got %v", st.resetCalls)
	}
}

func TestAsInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int64(42), 42}, {int(7), 7}, {[]byte("123"), 123}, {"456", 456}, {nil, 0},
	}
	for _, tc := range cases {
		if got := asInt64(tc.in); got != tc.want {
			t.Fatalf("asInt64(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
