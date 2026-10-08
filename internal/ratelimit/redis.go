package ratelimit

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
local ttl = tonumber(ARGV[5])

redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local count = redis.call('ZCARD', key)
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')

if count >= limit then
    local reset = now + window
    if #oldest > 0 then
        reset = tonumber(oldest[2]) + window
    end
    return {0, count, reset}
end

redis.call('ZADD', key, now, member)
redis.call('EXPIRE', key, ttl)
count = count + 1

oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
local reset = now + window
if #oldest > 0 then
    reset = tonumber(oldest[2]) + window
end

return {1, count, reset}
`)

type Decision struct {
	Allowed bool
	Count   int64
	ResetAt time.Time
}

type Store interface {
	Allow(context.Context, string, int64, time.Duration, time.Time) (Decision, error)
}

type RedisStore struct {
	client   *redis.Client
	sequence atomic.Uint64
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (store *RedisStore) Allow(ctx context.Context, key string, limit int64, window time.Duration, now time.Time) (Decision, error) {
	nowMillis := now.UnixMilli()
	member := fmt.Sprintf("%d-%d", now.UnixNano(), store.sequence.Add(1))
	result, err := slidingWindowScript.Run(
		ctx,
		store.client,
		[]string{key},
		nowMillis,
		window.Milliseconds(),
		limit,
		member,
		int64((window+time.Second-1)/time.Second),
	).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("execute rate limit script: %w", err)
	}
	if len(result) != 3 {
		return Decision{}, fmt.Errorf("unexpected rate limit script result length: %d", len(result))
	}

	allowed, ok := result[0].(int64)
	if !ok {
		return Decision{}, fmt.Errorf("unexpected allowed result type %T", result[0])
	}
	count, ok := result[1].(int64)
	if !ok {
		return Decision{}, fmt.Errorf("unexpected count result type %T", result[1])
	}
	resetMillis, ok := result[2].(int64)
	if !ok {
		return Decision{}, fmt.Errorf("unexpected reset result type %T", result[2])
	}

	return Decision{
		Allowed: allowed == 1,
		Count:   count,
		ResetAt: time.UnixMilli(resetMillis),
	}, nil
}
