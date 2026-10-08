package analytics

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDecodeClickEvent(t *testing.T) {
	timestamp := time.Date(2026, time.October, 8, 10, 30, 0, 123, time.UTC)

	event, err := decodeClickEvent(redis.XMessage{
		ID: "123-0",
		Values: map[string]interface{}{
			"code": "abc123",
			"ua":   "test-agent",
			"ref":  "https://example.com",
			"ip":   "hash",
			"ts":   timestamp.Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		t.Fatalf("decodeClickEvent() error = %v", err)
	}
	if event.ID != "123-0" || event.Code != "abc123" || event.Timestamp != timestamp {
		t.Fatalf("decoded event = %+v", event)
	}
	if event.UserAgent != "test-agent" || event.Referer != "https://example.com" || event.IPHash != "hash" {
		t.Fatalf("decoded metadata = %+v", event)
	}
}

func TestDecodeClickEventNormalizesMissingRefererAsDirect(t *testing.T) {
	event, err := decodeClickEvent(redis.XMessage{
		ID: "123-0",
		Values: map[string]interface{}{
			"code": "abc123",
			"ts":   "2026-10-08T10:30:00Z",
		},
	})
	if err != nil {
		t.Fatalf("decodeClickEvent() error = %v", err)
	}
	if event.Referer != "direct" {
		t.Fatalf("referer = %q, want direct", event.Referer)
	}
}

func TestDecodeClickEventRejectsInvalidTimestamp(t *testing.T) {
	_, err := decodeClickEvent(redis.XMessage{
		ID: "123-0",
		Values: map[string]interface{}{
			"code": "abc123",
			"ts":   "not-a-timestamp",
		},
	})
	if err == nil {
		t.Fatal("decodeClickEvent() error = nil, want invalid timestamp error")
	}
}

func TestNormalizeReferrer(t *testing.T) {
	tests := map[string]string{
		"":                                 "direct",
		"direct":                           "direct",
		"https://twitter.com/example":      "twitter",
		"https://www.linkedin.com/feed/":   "linkedin",
		"https://news.example.com/article": "other",
	}
	for input, want := range tests {
		if got := normalizeReferrer(input); got != want {
			t.Errorf("normalizeReferrer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeBrowser(t *testing.T) {
	tests := map[string]string{
		"Mozilla/5.0 Chrome/129.0 Safari/537.36":           "chrome",
		"Mozilla/5.0 Version/18.0 Safari/605.1.15":         "safari",
		"Mozilla/5.0 Chrome/129.0 Safari/537.36 Edg/129.0": "edge",
		"Mozilla/5.0 Firefox/131.0":                        "firefox",
		"Googlebot/2.1 (+http://www.google.com/bot.html)":  "bot",
	}
	for input, want := range tests {
		if got := normalizeBrowser(input); got != want {
			t.Errorf("normalizeBrowser(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUTCDateUsesUTCCalendarBoundary(t *testing.T) {
	timestamp := time.Date(2026, time.October, 2, 0, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	if got := utcDate(timestamp); got != "2026-10-01" {
		t.Fatalf("utcDate() = %q, want 2026-10-01", got)
	}
}
