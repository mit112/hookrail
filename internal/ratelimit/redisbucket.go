package ratelimit

import (
	"context"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaTokenBucket is the atomic token-bucket script run by RedisLimiter.
//
// KEYS[1] = bucket key
// ARGV[1] = rate (tokens/s)
// ARGV[2] = burst
// ARGV[3] = now (UnixMicro)
// ARGV[4] = ttl (seconds)
//
// The script clamps now >= last to prevent a lagging replica from adding
// tokens or rewinding state.  It does NOT use Redis TIME (which is
// classified as non-deterministic by Redis and errors on writes after it).
//nolint:gosec // Lua script — not a credential.
const luaTokenBucket = `
local rate=tonumber(ARGV[1]); local burst=tonumber(ARGV[2])
local now=tonumber(ARGV[3]); local ttl=tonumber(ARGV[4])
local s=redis.call('HMGET',KEYS[1],'tokens','last')
local tokens=tonumber(s[1]); local last=tonumber(s[2])
if tokens==nil then tokens=burst; last=now end
if now<last then now=last end                      -- never rewind
local elapsed=(now-last)/1000000.0                 -- micros -> seconds
if elapsed>0 then tokens=math.min(burst,tokens+elapsed*rate) end
local allowed=0
if tokens>=1.0 then tokens=tokens-1.0; allowed=1 end
redis.call('HSET',KEYS[1],'tokens',tokens,'last',now)
redis.call('PEXPIRE',KEYS[1],ttl*1000)
return allowed
`

// RedisLimiter is a Redis-backed token bucket using the atomic Lua script
// above.  An error return signals a limiter-command failure — the caller
// should fail open (use a local fallback).
type RedisLimiter struct {
	rdb      redis.Cmdable
	script   *redis.Script
	ttlFloor time.Duration
}

// NewRedisLimiter creates a new RedisLimiter.  rdb is the Redis client
// (must be a dedicated client, not the queue's).  ttlFloor sets a minimum
// key TTL so buckets never expire before one refill window.
func NewRedisLimiter(rdb redis.Cmdable, ttlFloor time.Duration) *RedisLimiter {
	return &RedisLimiter{rdb: rdb, script: redis.NewScript(luaTokenBucket), ttlFloor: ttlFloor}
}

// Allow checks whether one token can be consumed from the named bucket.
// It returns (allowed, nil) on success, or (false, err) on a limiter-
// command failure (e.g. Redis unavailable or timeout).  The caller should
// fail open on error.
func (l *RedisLimiter) Allow(ctx context.Context, key string, rate, burst float64, now time.Time) (bool, error) {
	ttl := ttlSeconds(rate, burst, l.ttlFloor)
	n, err := l.script.Run(ctx, l.rdb, []string{"hookrail:rl:" + key},
		rate, burst, now.UnixMicro(), ttl).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ttlSeconds returns the key TTL in seconds: at least one full refill
// window (ceil(burst/rate)), but no less than floor.
func ttlSeconds(rate, burst float64, floor time.Duration) int {
	refill := int(math.Ceil(burst / rate)) // seconds to refill from empty to full
	if f := int(floor.Seconds()); f > refill {
		return f
	}
	return refill
}
