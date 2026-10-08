package config

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisOptions_MapsEnvironmentConfiguration(t *testing.T) {
	env := &Env{
		RedisAddr:         "redis.internal:6380",
		RedisPassword:     "secret",
		RedisDB:           3,
		RedisPoolSize:     80,
		RedisMinIdleConns: 12,
		RedisDialTimeout:  2 * time.Second,
		RedisReadTimeout:  4 * time.Second,
		RedisWriteTimeout: 6 * time.Second,
	}

	options := redisOptions(env)
	if options.Addr != env.RedisAddr || options.Password != env.RedisPassword || options.DB != env.RedisDB {
		t.Fatalf("Redis identity options were not mapped: %+v", options)
	}
	if options.PoolSize != env.RedisPoolSize || options.MinIdleConns != env.RedisMinIdleConns {
		t.Fatalf("Redis pool options were not mapped: %+v", options)
	}
	if options.DialTimeout != env.RedisDialTimeout || options.ReadTimeout != env.RedisReadTimeout || options.WriteTimeout != env.RedisWriteTimeout {
		t.Fatalf("Redis timeout options were not mapped: %+v", options)
	}
}

func TestNewRedisClient_FailsFastWhenRedisIsUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}

	env := &Env{
		RedisAddr:         address,
		RedisPoolSize:     1,
		RedisMinIdleConns: 0,
		RedisDialTimeout:  50 * time.Millisecond,
		RedisReadTimeout:  50 * time.Millisecond,
		RedisWriteTimeout: 50 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	client, err := NewRedisClient(ctx, env)
	if err == nil {
		_ = client.Close()
		t.Fatal("NewRedisClient() error = nil, want startup failure")
	}
	if client != nil {
		t.Fatal("NewRedisClient() returned a client after failed Ping")
	}
}

func TestCloseRedisClient_ReleasesClient(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	if err := CloseRedisClient(client); err != nil {
		t.Fatalf("CloseRedisClient() error = %v", err)
	}
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("Ping() after CloseRedisClient() error = nil, want closed client error")
	}
}
