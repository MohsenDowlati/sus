package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/ratelimit"
)

type channelPublisher struct {
	events chan ClickEvent
	err    error
}

func (publisher *channelPublisher) Publish(_ context.Context, event ClickEvent) error {
	if publisher.events != nil {
		publisher.events <- event
	}
	return publisher.err
}

type blockingPublisher struct {
	started chan struct{}
	release chan struct{}
}

func (publisher *blockingPublisher) Publish(context.Context, ClickEvent) error {
	close(publisher.started)
	<-publisher.release
	return nil
}

func TestTracker_ExtractsMetadataAndHashesCanonicalClientIP(t *testing.T) {
	clientIPs, err := ratelimit.NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	publisher := &channelPublisher{events: make(chan ClickEvent, 1)}
	salt := "analytics-test-salt-with-at-least-32-characters"
	tracker := NewTracker(publisher, clientIPs, salt, slog.Default(), 1)
	defer closeTracker(t, tracker)
	request := httptest.NewRequest("GET", "/abc123", nil)
	request.RemoteAddr = "10.0.0.2:4321"
	request.Header.Set("X-Forwarded-For", "::ffff:198.51.100.20")
	request.Header.Set("User-Agent", "analytics-test-agent")
	request.Header.Set("Referer", "https://referrer.example/page")
	before := time.Now().UTC()

	tracker.Track(request, "abc123")

	select {
	case event := <-publisher.events:
		after := time.Now().UTC()
		digest := sha256.Sum256([]byte("198.51.100.20" + salt))
		wantHash := hex.EncodeToString(digest[:])
		if event.Code != "abc123" || event.UserAgent != "analytics-test-agent" || event.Referer != "https://referrer.example/page" {
			t.Fatalf("unexpected event metadata: %+v", event)
		}
		if event.IPHash != wantHash {
			t.Fatalf("IPHash = %q, want %q", event.IPHash, wantHash)
		}
		if event.IPHash == "198.51.100.20" || strings.Contains(event.IPHash, "198.51.100.20") {
			t.Fatal("event contains the original client IP")
		}
		if event.Timestamp.Before(before) || event.Timestamp.After(after) {
			t.Fatalf("Timestamp = %s, want between %s and %s", event.Timestamp, before, after)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for analytics event")
	}
}

func TestTracker_TrackDoesNotWaitForPublisher(t *testing.T) {
	publisher := &blockingPublisher{started: make(chan struct{}), release: make(chan struct{})}
	clientIPs, _ := ratelimit.NewClientIPResolver(nil)
	tracker := NewTracker(publisher, clientIPs, "analytics-test-salt-with-at-least-32-characters", slog.Default(), 1)
	request := httptest.NewRequest("GET", "/abc123", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	returned := make(chan struct{})

	go func() {
		tracker.Track(request, "abc123")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Track() waited for the publisher")
	}
	select {
	case <-publisher.started:
	case <-time.After(time.Second):
		t.Fatal("publisher did not receive queued event")
	}
	close(publisher.release)
	closeTracker(t, tracker)
}

func TestTracker_PublishFailureDoesNotLogSensitiveRequestValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	publisher := &channelPublisher{events: make(chan ClickEvent, 1), err: errors.New("Redis unavailable")}
	clientIPs, _ := ratelimit.NewClientIPResolver(nil)
	tracker := NewTracker(publisher, clientIPs, "analytics-test-salt-with-at-least-32-characters", logger, 1)
	request := httptest.NewRequest("GET", "/abc123", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("Authorization", "Bearer highly-sensitive-token")

	tracker.Track(request, "abc123")
	closeTracker(t, tracker)

	output := logs.String()
	if !strings.Contains(output, "failed to publish click analytics event") {
		t.Fatalf("failure log missing: %s", output)
	}
	for _, sensitive := range []string{"198.51.100.20", "highly-sensitive-token", "Authorization"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("failure log contains sensitive value %q: %s", sensitive, output)
		}
	}
}

func closeTracker(t *testing.T, tracker *Tracker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tracker.Close(ctx); err != nil {
		t.Fatalf("Tracker.Close() error = %v", err)
	}
}
