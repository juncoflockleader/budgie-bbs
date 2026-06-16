package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// maxRetryAfter returns the longest current lockout across the given keys on a
// limiter, or 0 if all are allowed.
func maxRetryAfter(l *failureLimiter, keys ...string) time.Duration {
	var worst time.Duration
	for _, k := range keys {
		if w := l.retryAfter(k); w > worst {
			worst = w
		}
	}
	return worst
}

// writeRateLimited sends a 429 with a Retry-After header (seconds).
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later", true)
}

// failureLimiter is an in-memory, per-key brute-force limiter. A key (typically
// a client IP or an account name) is locked out once it accumulates `threshold`
// failures within a rolling `window`; the lockout lasts `lockout` and escalates
// up to 8x for repeat offenders. Successful attempts reset the key. It is safe
// for concurrent use and self-prunes expired entries.
//
// This is process-local: each node limits independently. That is intentionally
// simple (no external store); it still meaningfully throttles online guessing.
// For cluster-wide limits, back this with Redis later.
type failureLimiter struct {
	mu        sync.Mutex
	entries   map[string]*failureEntry
	threshold int
	window    time.Duration
	lockout   time.Duration
	now       func() time.Time
	lastPrune time.Time
}

type failureEntry struct {
	count       int
	windowStart time.Time
	lockUntil   time.Time
	strikes     int
}

func newFailureLimiter(threshold int, window, lockout time.Duration) *failureLimiter {
	return &failureLimiter{
		entries:   make(map[string]*failureEntry),
		threshold: threshold,
		window:    window,
		lockout:   lockout,
		now:       time.Now,
	}
}

// retryAfter returns how long the key must wait before another attempt is
// allowed, or 0 if it is currently allowed.
func (l *failureLimiter) retryAfter(key string) time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return 0
	}
	now := l.now()
	if now.Before(e.lockUntil) {
		return e.lockUntil.Sub(now)
	}
	return 0
}

// fail records a failed attempt for the key, locking it out once the threshold
// is reached within the window. Returns true when this failure triggers (or
// extends) a lockout.
func (l *failureLimiter) fail(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	e := l.entries[key]
	if e == nil {
		e = &failureEntry{windowStart: now}
		l.entries[key] = e
	}
	if now.Sub(e.windowStart) > l.window {
		e.count = 0
		e.windowStart = now
	}
	e.count++
	if e.count >= l.threshold {
		mult := time.Duration(1) << min(e.strikes, 3) // 1x,2x,4x,8x
		e.lockUntil = now.Add(l.lockout * mult)
		e.count = 0
		e.windowStart = now
		e.strikes++
		return true
	}
	return false
}

// (min is a Go builtin as of 1.21.)

// reset clears a key after a successful attempt.
func (l *failureLimiter) reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// pruneLocked drops entries that are no longer locked out and whose failure
// window has elapsed. Called under the lock, at most once per window.
func (l *failureLimiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < l.window {
		return
	}
	l.lastPrune = now
	for k, e := range l.entries {
		if now.After(e.lockUntil) && now.Sub(e.windowStart) > l.window {
			delete(l.entries, k)
		}
	}
}

