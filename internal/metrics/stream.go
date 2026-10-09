package metrics

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type streamClient interface {
	XInfoGroups(context.Context, string) *redis.XInfoGroupsCmd
	XLen(context.Context, string) *redis.IntCmd
	XRangeN(context.Context, string, string, string, int64) *redis.XMessageSliceCmd
	XRevRangeN(context.Context, string, string, string, int64) *redis.XMessageSliceCmd
}

// StreamCollector measures entries awaiting delivery plus delivered entries
// awaiting acknowledgment. It queries Redis at scrape time to avoid stale values.
type StreamCollector struct {
	client        streamClient
	stream, group string
	desc          *prometheus.Desc
}

func NewStreamCollector(client streamClient, stream, group string) *StreamCollector {
	return &StreamCollector{client: client, stream: stream, group: group, desc: prometheus.NewDesc("shortener_stream_lag", "Unconsumed Redis stream entries: undelivered entries plus pending acknowledgments.", []string{"stream", "consumer_group"}, nil)}
}

func (c *StreamCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c *StreamCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	backlog, err := c.backlog(ctx)
	if err != nil {
		slog.Warn("cannot collect stream backlog", "stream", c.stream, "consumer_group", c.group, "error", err)
		return // Omit unavailable data rather than report a false zero.
	}
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(backlog), c.stream, c.group)
}

func (c *StreamCollector) backlog(ctx context.Context) (int64, error) {
	groups, err := c.client.XInfoGroups(ctx, c.stream).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return 0, nil
		}
		return 0, err
	}
	for _, group := range groups {
		if group.Name != c.group {
			continue
		}
		lag := group.Lag
		if lag < 0 {
			// Redis reports unknown lag after trimming/deleting entries or SETID.
			lag, err = c.undelivered(ctx, group.LastDeliveredID)
			if err != nil {
				return 0, err
			}
		}
		return lag + group.Pending, nil
	}
	// Before a worker creates its group, all entries await consumption.
	return c.client.XLen(ctx, c.stream).Result()
}

// Redis can report unknown group lag. Count existing entries with bounded range
// pages, stopping at the tail captured before scanning so new writes cannot
// extend the scan indefinitely.
func (c *StreamCollector) undelivered(ctx context.Context, after string) (int64, error) {
	tail, err := c.client.XRevRangeN(ctx, c.stream, "+", "-", 1).Result()
	if err != nil {
		return 0, err
	}
	if len(tail) == 0 {
		return 0, nil
	}
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entries, err := c.client.XRangeN(ctx, c.stream, "("+after, tail[0].ID, 1000).Result()
		if err != nil {
			return 0, err
		}
		count += int64(len(entries))
		if len(entries) < 1000 {
			return count, nil
		}
		after = entries[len(entries)-1].ID
	}
}
