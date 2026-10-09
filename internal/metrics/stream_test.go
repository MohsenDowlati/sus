package metrics

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func TestStreamBacklogIncludesUndeliveredAndPending(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	collector := NewStreamCollector(unknownLagClient{client}, "stream:clicks", "analytics-workers")
	check := func(want int64) {
		t.Helper()
		got, err := collector.backlog(ctx)
		if err != nil || got != want {
			t.Fatalf("backlog = %d, %v; want %d", got, err, want)
		}
	}
	check(0)
	for i := 0; i < 3; i++ {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: collector.stream, Values: map[string]interface{}{"code": "abc"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	check(3) // No group yet.
	if err := client.XGroupCreate(ctx, collector.stream, collector.group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	check(3)
	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: collector.group, Consumer: "worker-1", Streams: []string{collector.stream, ">"}, Count: 2}).Result()
	if err != nil {
		t.Fatal(err)
	}
	check(3) // Two pending plus one undelivered.
	if err := client.XAck(ctx, collector.stream, collector.group, streams[0].Messages[0].ID).Err(); err != nil {
		t.Fatal(err)
	}
	check(2)
}

// Miniredis reports stream length as group lag even after delivery. Override
// that field to exercise Redis's documented unknown-lag fallback, while using
// actual Redis stream commands for delivery, acknowledgments and range reads.
type unknownLagClient struct{ *redis.Client }

func (c unknownLagClient) XInfoGroups(ctx context.Context, stream string) *redis.XInfoGroupsCmd {
	cmd := c.Client.XInfoGroups(ctx, stream)
	groups, err := cmd.Result()
	if err == nil {
		for i := range groups {
			groups[i].Lag = -1
		}
		cmd.SetVal(groups)
	}
	return cmd
}

func TestStreamBacklogUsesKnownGroupLag(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	collector := NewStreamCollector(knownLagClient{client}, "stream:clicks", "analytics-workers")
	got, err := collector.backlog(context.Background())
	if err != nil || got != 7 {
		t.Fatalf("backlog = %d, %v; want 7", got, err)
	}
}

type knownLagClient struct{ *redis.Client }

func (c knownLagClient) XInfoGroups(ctx context.Context, _ string) *redis.XInfoGroupsCmd {
	cmd := redis.NewXInfoGroupsCmd(ctx, "stream:clicks")
	cmd.SetVal([]redis.XInfoGroup{{Name: "other", Lag: 99}, {Name: "analytics-workers", Lag: 4, Pending: 3}})
	return cmd
}


func TestStreamGaugeReadsCurrentStateOnEveryScrape(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr:server.Addr()})
	defer client.Close()
	collector := NewStreamCollector(unknownLagClient{client}, "stream:clicks", "analytics-workers")
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	ctx := context.Background()
	check := func(want float64) {
		t.Helper()
		families, err := registry.Gather()
		if err != nil || len(families) != 1 || len(families[0].Metric) != 1 { t.Fatalf("gauge scrape: %v", err) }
		metric := families[0].Metric[0]
		if metric.GetGauge().GetValue() != want { t.Fatalf("gauge = %v, want %v", metric.GetGauge().GetValue(), want) }
		for _, label := range metric.Label { if label.GetName() != "stream" && label.GetName() != "consumer_group" { t.Fatal("unexpected gauge dimension") } }
	}
	check(0)
	var ids []string
	for i := 1; i <= 2; i++ {
		id, err := client.XAdd(ctx, &redis.XAddArgs{Stream:collector.stream, Values:map[string]any{"code":"abc"}}).Result()
		if err != nil { t.Fatal(err) }
		ids = append(ids, id)
		check(float64(i))
	}
	if err := client.XGroupCreate(ctx, collector.stream, collector.group, "0").Err(); err != nil { t.Fatal(err) }
	if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group:collector.group, Consumer:"worker", Streams:[]string{collector.stream, ">"}, Count:2}).Err(); err != nil { t.Fatal(err) }
	check(2)
	for i, id := range ids {
		if err := client.XAck(ctx, collector.stream, collector.group, id).Err(); err != nil { t.Fatal(err) }
		check(float64(1-i))
	}
}
