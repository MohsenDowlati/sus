package analytics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type StreamMessageSource interface {
	EnsureGroup(context.Context, string, string) error
	Read(context.Context, string, string, string, string, int64, time.Duration) ([]redis.XMessage, error)
	Claim(context.Context, string, string, string, time.Duration, string, int64) ([]redis.XMessage, string, error)
	Ack(context.Context, string, string, ...string) error
}

type RedisStreamConsumer struct {
	client *redis.Client
}

func NewRedisStreamConsumer(client *redis.Client) *RedisStreamConsumer {
	return &RedisStreamConsumer{client: client}
}

func (consumer *RedisStreamConsumer) EnsureGroup(ctx context.Context, stream, group string) error {
	err := consumer.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create Redis consumer group: %w", err)
	}
	return nil
}

func (consumer *RedisStreamConsumer) Read(ctx context.Context, stream, group, name, start string, count int64, block time.Duration) ([]redis.XMessage, error) {
	streams, err := consumer.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: name,
		Streams:  []string{stream, start},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return streams[0].Messages, nil
}

func (consumer *RedisStreamConsumer) Claim(ctx context.Context, stream, group, name string, minIdle time.Duration, start string, count int64) ([]redis.XMessage, string, error) {
	messages, next, err := consumer.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: name,
		MinIdle:  minIdle,
		Start:    start,
		Count:    count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, "0-0", nil
	}
	return messages, next, err
}

func (consumer *RedisStreamConsumer) Ack(ctx context.Context, stream, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return consumer.client.XAck(ctx, stream, group, ids...).Err()
}
