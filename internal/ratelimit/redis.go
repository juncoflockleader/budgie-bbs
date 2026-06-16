package ratelimit

import (
	"context"
	"strconv"
	"time"
)

// Doer is the minimal Redis command interface the RedisStore needs. The
// redisconn.Client satisfies it; keeping it local avoids importing redisconn.
type Doer interface {
	Do(ctx context.Context, args ...any) (any, error)
}

// failScript atomically applies the failure policy on Redis so it is correct
// even with concurrent requests across nodes:
//
//	KEYS[1]=counter, KEYS[2]=lockout; ARGV[1]=threshold, ARGV[2]=windowMs, ARGV[3]=lockoutMs
//
// Returns the remaining lockout in ms (the existing lock if already locked, the
// new lock if this failure trips the threshold) or 0.
const failScript = `
local locked = redis.call('PTTL', KEYS[2])
if locked and locked > 0 then return locked end
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if c >= tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
  redis.call('DEL', KEYS[1])
  return tonumber(ARGV[3])
end
return 0
`

// RedisStore is a cluster-wide ratelimit.Store backed by Redis. Counters and
// lockouts use native Redis TTLs, so no sweeping is needed. Each call is bounded
// by a short timeout; the Limiter treats errors as fail-open (per-node only).
type RedisStore struct {
	doer    Doer
	prefix  string
	timeout time.Duration
}

// NewRedisStore builds a store keyed under "<prefix>:ratelimit". A single store
// can be shared by limiters with different policies — the policy is passed per
// Fail call and the (handler-supplied) keys keep their counters separate.
func NewRedisStore(doer Doer, prefix string) *RedisStore {
	if prefix == "" {
		prefix = "budgie"
	}
	return &RedisStore{doer: doer, prefix: prefix + ":ratelimit", timeout: 750 * time.Millisecond}
}

func (s *RedisStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.timeout)
}

func (s *RedisStore) counterKey(key string) string { return s.prefix + ":c:" + key }
func (s *RedisStore) lockKey(key string) string    { return s.prefix + ":l:" + key }

func (s *RedisStore) Fail(key string, threshold int, window, lockout time.Duration) (time.Duration, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.doer.Do(ctx, "EVAL", failScript, "2", s.counterKey(key), s.lockKey(key),
		threshold, window.Milliseconds(), lockout.Milliseconds())
	if err != nil {
		return 0, err
	}
	return time.Duration(asInt64(res)) * time.Millisecond, nil
}

func (s *RedisStore) RetryAfter(key string) (time.Duration, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.doer.Do(ctx, "PTTL", s.lockKey(key))
	if err != nil {
		return 0, err
	}
	ms := asInt64(res)
	if ms <= 0 { // -2 no key, -1 no expiry, 0 expired
		return 0, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (s *RedisStore) Reset(key string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.doer.Do(ctx, "DEL", s.counterKey(key), s.lockKey(key))
	return err
}

// asInt64 coerces a RESP reply (int64 for integers, []byte/string for bulk) to
// an int64.
func asInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case []byte:
		n, _ := strconv.ParseInt(string(t), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}
