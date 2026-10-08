package analytics

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const ClickStream = "stream:clicks"

type RedisStreamPublisher struct {
	client *redis.Client
	stream string
}

func NewRedisStreamPublisher(client *redis.Client) *RedisStreamPublisher {
	return NewRedisStreamPublisherForStream(client, ClickStream)
}

func NewRedisStreamPublisherForStream(client *redis.Client, stream string) *RedisStreamPublisher {
	return &RedisStreamPublisher{client: client, stream: stream}
}

func (publisher *RedisStreamPublisher) Publish(ctx context.Context, event ClickEvent) error {
	return publisher.client.XAdd(ctx, &redis.XAddArgs{
		Stream: publisher.stream,
		Values: map[string]any{
			"code": event.Code,
			"ua":   event.UserAgent,
			"ref":  event.Referer,
			"ip":   event.IPHash,
			"ts":   event.Timestamp.UTC().Format(time.RFC3339Nano),
		},
	}).Err()
}
