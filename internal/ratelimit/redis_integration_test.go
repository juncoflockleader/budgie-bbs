package ratelimit_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/ratelimit"
	"github.com/juncoflockleader/budgie-bbs/internal/redisconn"
)

// TestRedisStoreIntegration validates the RedisStore against a real Redis. It
// skips unless BUDGIE_TEST_REDIS_URL is set (CI provides a redis service).
func TestRedisStoreIntegration(t *testing.T) {
	url := os.Getenv("BUDGIE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set BUDGIE_TEST_REDIS_URL to run the Redis ratelimit integration test")
	}
	client, err := redisconn.NewClient(url)
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Unique prefix per run so repeated runs don't collide.
	store := ratelimit.NewRedisStore(client, fmt.Sprintf("budgietest-%d", time.Now().UnixNano()))
	const (
		threshold = 3
		window    = time.Minute
		lockout   = 5 * time.Minute
	)
	key := "ip:198.51.100.7"

	// Failures under the threshold do not lock.
	for i := 0; i < threshold-1; i++ {
		d, err := store.Fail(key, threshold, window, lockout)
		if err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
		if d != 0 {
			t.Fatalf("attempt %d should not lock, got %v", i, d)
		}
	}
	// The threshold-th failure trips the lockout.
	d, err := store.Fail(key, threshold, window, lockout)
	if err != nil {
		t.Fatalf("tripping fail: %v", err)
	}
	if d <= 0 {
		t.Fatalf("expected a lockout after %d failures, got %v", threshold, d)
	}
	// RetryAfter reflects the lockout, and it is shared (any node would see it).
	if ra, err := store.RetryAfter(key); err != nil || ra <= 0 {
		t.Fatalf("RetryAfter after lockout = %v, err=%v; want > 0", ra, err)
	}
	// Reset clears it.
	if err := store.Reset(key); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if ra, err := store.RetryAfter(key); err != nil || ra != 0 {
		t.Fatalf("RetryAfter after reset = %v, err=%v; want 0", ra, err)
	}

	// A different key is independent.
	if ra, err := store.RetryAfter("ip:203.0.113.9"); err != nil || ra != 0 {
		t.Fatalf("unrelated key should be unlocked, got %v err=%v", ra, err)
	}
}
