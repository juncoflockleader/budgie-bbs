package httpapi

import (
	"testing"
	"time"
)

func TestFailureLimiterLocksOutAndRecovers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newFailureLimiter(3, time.Minute, 5*time.Minute)
	l.now = func() time.Time { return now }

	// Under threshold: still allowed.
	for i := 0; i < 2; i++ {
		l.fail("k")
		if w := l.retryAfter("k"); w != 0 {
			t.Fatalf("attempt %d should not be locked, got %v", i, w)
		}
	}
	// The 3rd failure triggers the lockout.
	l.fail("k")
	if w := l.retryAfter("k"); w <= 0 {
		t.Fatal("expected lockout after threshold failures")
	}
	// After the lockout elapses, it's allowed again.
	now = now.Add(6 * time.Minute)
	if w := l.retryAfter("k"); w != 0 {
		t.Fatalf("expected lockout to expire, got %v", w)
	}
}

func TestFailureLimiterResetClears(t *testing.T) {
	l := newFailureLimiter(3, time.Minute, time.Minute)
	l.fail("k")
	l.fail("k")
	l.reset("k")
	// A success reset the counter, so two more failures still don't lock out.
	l.fail("k")
	l.fail("k")
	if w := l.retryAfter("k"); w != 0 {
		t.Fatalf("reset should have cleared the counter, got %v", w)
	}
}

func TestFailureLimiterKeysAreIndependent(t *testing.T) {
	l := newFailureLimiter(2, time.Minute, time.Minute)
	l.fail("a")
	l.fail("a") // locks "a"
	if l.retryAfter("a") == 0 {
		t.Fatal("a should be locked")
	}
	if l.retryAfter("b") != 0 {
		t.Fatal("b must be independent of a")
	}
}
