package config

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(ctx context.Context, env *Env) (*redis.Client, error) {
	client := redis.NewClient(redisOptions(env))
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Redis at %s: %w", env.RedisAddr, err)
	}
	return client, nil
}

func redisOptions(env *Env) *redis.Options {
	return &redis.Options{
		Addr:         env.RedisAddr,
		Password:     env.RedisPassword,
		DB:           env.RedisDB,
		PoolSize:     env.RedisPoolSize,
		MinIdleConns: env.RedisMinIdleConns,
		DialTimeout:  env.RedisDialTimeout,
		ReadTimeout:  env.RedisReadTimeout,
		WriteTimeout: env.RedisWriteTimeout,
	}
}

func CloseRedisClient(client *redis.Client) error {
	if client == nil {
		return nil
	}
	return client.Close()
}
