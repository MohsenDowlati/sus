package cache

import (
	"context"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/metrics"
	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

func metricValue(counter prometheus.Counter) float64 {
	m := &dto.Metric{}
	_ = counter.Write(m)
	return m.GetCounter().GetValue()
}

func TestCacheMetricsIncludeNegativeHitsAndExcludeErrors(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	cache := NewRedis(client)
	ctx := context.Background()
	hits, misses := metricValue(metrics.CacheHits), metricValue(metrics.CacheMisses)
	if _, found, err := cache.Get(ctx, "link:missing"); err != nil || found {
		t.Fatal("expected cache miss")
	}
	if err := cache.Set(ctx, "link:negative", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, found, err := cache.Get(ctx, "link:negative"); err != nil || !found {
		t.Fatal("negative entry must be a hit")
	}
	_ = client.Close()
	if _, _, err := cache.Get(ctx, "link:error"); err == nil {
		t.Fatal("expected closed-client error")
	}
	if metricValue(metrics.CacheHits) != hits+1 || metricValue(metrics.CacheMisses) != misses+1 {
		t.Fatal("incorrect cache counters")
	}
}
