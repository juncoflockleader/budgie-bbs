package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterLocksOutAndRecovers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := New(3, time.Minute, 5*time.Minute)
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 2; i++ {
		l.Fail("k")
		if w := l.RetryAfter("k"); w != 0 {
			t.Fatalf("attempt %d should not be locked, got %v", i, w)
		}
	}
	l.Fail("k") // 3rd failure → lockout
	if w := l.RetryAfter("k"); w <= 0 {
		t.Fatal("expected lockout after threshold failures")
	}
	now = now.Add(6 * time.Minute)
	if w := l.RetryAfter("k"); w != 0 {
		t.Fatalf("expected lockout to expire, got %v", w)
	}
}

func TestLimiterResetClears(t *testing.T) {
	l := New(3, time.Minute, time.Minute)
	l.Fail("k")
	l.Fail("k")
	l.Reset("k")
	l.Fail("k")
	l.Fail("k")
	if w := l.RetryAfter("k"); w != 0 {
		t.Fatalf("reset should have cleared the counter, got %v", w)
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := New(2, time.Minute, time.Minute)
	l.Fail("a")
	l.Fail("a")
	if l.RetryAfter("a") == 0 {
		t.Fatal("a should be locked")
	}
	if l.RetryAfter("b") != 0 {
		t.Fatal("b must be independent of a")
	}
}

func TestLimiterDisabledWhenThresholdNonPositive(t *testing.T) {
	l := New(0, time.Minute, time.Minute)
	for i := 0; i < 100; i++ {
		l.Fail("k")
	}
	if l.RetryAfter("k") != 0 {
		t.Fatal("a non-positive threshold should disable limiting")
	}
}
