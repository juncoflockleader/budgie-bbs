// Package ratelimit provides a small in-memory, per-key brute-force limiter
// shared by the HTTP and SSH transports. A key (typically a client IP or an
// account name) is locked out once it accumulates `threshold` failures within a
// rolling `window`; the lockout lasts `lockout` and escalates up to 8x for
// repeat offenders. Successful attempts reset the key.
//
// It is process-local (each node limits independently) and self-prunes expired
// entries. For cluster-wide limits, back this with Redis.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu        sync.Mutex
	entries   map[string]*entry
	threshold int
	window    time.Duration
	lockout   time.Duration
	now       func() time.Time
	lastPrune time.Time
	store     Store // optional shared (cluster-wide) backend
}

// Store is an optional shared backend (e.g. Redis) that makes limiting
// cluster-wide: every node consults the same counters/lockouts so an attacker
// cannot reset the budget by spreading attempts across nodes. The local
// in-memory state is always kept too, so a Store outage degrades to per-node
// limiting rather than no limiting (the Limiter fails open to local on errors).
type Store interface {
	// Fail records a failure for key and returns the remaining lockout (>0 when
	// the key is now locked) using the given policy.
	Fail(key string, threshold int, window, lockout time.Duration) (time.Duration, error)
	// RetryAfter returns the remaining lockout for key, or 0 if not locked.
	RetryAfter(key string) (time.Duration, error)
	// Reset clears the key after a successful attempt.
	Reset(key string) error
}

// SetStore attaches a shared backend, upgrading this limiter to cluster-wide.
func (l *Limiter) SetStore(s Store) {
	if l != nil {
		l.store = s
	}
}

type entry struct {
	count       int
	windowStart time.Time
	lockUntil   time.Time
	strikes     int
}

// New builds a limiter. A non-positive threshold disables limiting.
func New(threshold int, window, lockout time.Duration) *Limiter {
	return &Limiter{
		entries:   make(map[string]*entry),
		threshold: threshold,
		window:    window,
		lockout:   lockout,
		now:       time.Now,
	}
}

// RetryAfter returns how long the key must wait before another attempt is
// allowed, or 0 if it is currently allowed.
func (l *Limiter) RetryAfter(key string) time.Duration {
	if l == nil || l.threshold <= 0 {
		return 0
	}
	d := l.retryAfterLocal(key)
	if l.store != nil {
		if rd, err := l.store.RetryAfter(key); err == nil && rd > d {
			d = rd
		}
	}
	return d
}

func (l *Limiter) retryAfterLocal(key string) time.Duration {
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

// MaxRetryAfter returns the longest current lockout across the given keys.
func (l *Limiter) MaxRetryAfter(keys ...string) time.Duration {
	var worst time.Duration
	for _, k := range keys {
		if w := l.RetryAfter(k); w > worst {
			worst = w
		}
	}
	return worst
}

// Fail records a failed attempt, locking the key out once the threshold is hit
// within the window. Returns true when this failure triggers/extends a lockout.
func (l *Limiter) Fail(key string) bool {
	if l == nil || l.threshold <= 0 {
		return false
	}
	local := l.failLocal(key)
	if l.store == nil {
		return local
	}
	// Fail open to per-node limiting if the shared store errors.
	d, err := l.store.Fail(key, l.threshold, l.window, l.lockout)
	if err != nil {
		return local
	}
	return local || d > 0
}

func (l *Limiter) failLocal(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	e := l.entries[key]
	if e == nil {
		e = &entry{windowStart: now}
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

// Reset clears a key after a successful attempt.
func (l *Limiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
	if l.store != nil {
		_ = l.store.Reset(key)
	}
}

// SetClock overrides the time source (tests only).
func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

func (l *Limiter) pruneLocked(now time.Time) {
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
