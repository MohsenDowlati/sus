package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client *redis.Client
}

func NewRedis(client *redis.Client) *Redis {
	return &Redis{client: client}
}

func (cache *Redis) Get(ctx context.Context, key string) (string, bool, error) {
	value, err := cache.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (cache *Redis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return cache.client.Set(ctx, key, value, ttl).Err()
}
