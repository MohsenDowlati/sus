package config

import (
	"testing"
	"time"
)

func TestNewEnvAnalyticsConfiguration(t *testing.T) {
	t.Setenv("REDIS_ANALYTICS_STREAM", "stream:test-clicks")
	t.Setenv("REDIS_ANALYTICS_CONSUMER_GROUP", "test-workers")
	t.Setenv("REDIS_ANALYTICS_CONSUMER_NAME", "worker-7")
	t.Setenv("ANALYTICS_IP_SALT", "analytics-test-salt-with-at-least-32-characters")
	t.Setenv("ANALYTICS_BATCH_SIZE", "250")
	t.Setenv("ANALYTICS_POLL_TIMEOUT", "750ms")
	t.Setenv("ANALYTICS_MAX_RETRIES", "5")
	t.Setenv("CLICK_EVENTS_RETENTION_DAYS", "90")
	t.Setenv("ANALYTICS_MAX_QUERY_DAYS", "180")

	env := NewEnv()
	if env.RedisAnalyticsStream != "stream:test-clicks" {
		t.Fatalf("RedisAnalyticsStream = %q", env.RedisAnalyticsStream)
	}
	if env.RedisAnalyticsConsumerGroup != "test-workers" || env.RedisAnalyticsConsumerName != "worker-7" {
		t.Fatalf("consumer = %q/%q", env.RedisAnalyticsConsumerGroup, env.RedisAnalyticsConsumerName)
	}
	if env.AnalyticsBatchSize != 250 || env.AnalyticsPollTimeout != 750*time.Millisecond {
		t.Fatalf("batch/poll = %d/%s", env.AnalyticsBatchSize, env.AnalyticsPollTimeout)
	}
	if env.AnalyticsMaxRetries != 5 || env.ClickEventsRetentionDays != 90 || env.AnalyticsMaxQueryDays != 180 {
		t.Fatalf("retry/retention/query = %d/%d/%d", env.AnalyticsMaxRetries, env.ClickEventsRetentionDays, env.AnalyticsMaxQueryDays)
	}
	if env.ClickEventsRetention() != 90*24*time.Hour {
		t.Fatalf("ClickEventsRetention() = %s", env.ClickEventsRetention())
	}
}

func TestNewEnvAnalyticsDefaults(t *testing.T) {
	t.Setenv("REDIS_ANALYTICS_STREAM", "")
	t.Setenv("REDIS_ANALYTICS_CONSUMER_GROUP", "")
	t.Setenv("ANALYTICS_BATCH_SIZE", "")

	env := NewEnv()
	if env.RedisAnalyticsStream != "stream:clicks" {
		t.Fatalf("RedisAnalyticsStream = %q, want stream:clicks", env.RedisAnalyticsStream)
	}
	if env.RedisAnalyticsConsumerGroup != "analytics-workers" {
		t.Fatalf("RedisAnalyticsConsumerGroup = %q, want analytics-workers", env.RedisAnalyticsConsumerGroup)
	}
	if env.AnalyticsBatchSize != 100 {
		t.Fatalf("AnalyticsBatchSize = %d, want 100", env.AnalyticsBatchSize)
	}
}
