package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStreamPublisher_XAddFields(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	publisher := NewRedisStreamPublisher(client)
	timestamp := time.Date(2026, time.October, 8, 12, 30, 0, 123, time.UTC)

	err := publisher.Publish(context.Background(), ClickEvent{
		Code:      "abc123",
		UserAgent: "test-agent",
		Referer:   "https://referrer.example",
		IPHash:    "hashed-ip",
		Timestamp: timestamp,
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	messages, err := client.XRange(context.Background(), ClickStream, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stream messages = %d, want 1", len(messages))
	}
	want := map[string]any{
		"code": "abc123",
		"ua":   "test-agent",
		"ref":  "https://referrer.example",
		"ip":   "hashed-ip",
		"ts":   timestamp.Format(time.RFC3339Nano),
	}
	for field, wantValue := range want {
		if got := messages[0].Values[field]; got != wantValue {
			t.Fatalf("stream field %s = %v, want %v", field, got, wantValue)
		}
	}
}

func TestRedisStreamPublisher_UsesConfiguredStream(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	publisher := NewRedisStreamPublisherForStream(client, "stream:custom-clicks")

	err := publisher.Publish(context.Background(), ClickEvent{
		Code:      "abc123",
		Timestamp: time.Date(2026, time.October, 8, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if length, err := client.XLen(context.Background(), "stream:custom-clicks").Result(); err != nil || length != 1 {
		t.Fatalf("custom stream length = %d, error = %v, want 1", length, err)
	}
}
