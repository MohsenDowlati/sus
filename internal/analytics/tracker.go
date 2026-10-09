package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/MohsenDowlati/shorts/internal/ratelimit"
	"go.opentelemetry.io/otel/trace"
)

const publishTimeout = 2 * time.Second

type ClickEvent struct {
	Code      string
	UserAgent string
	Referer   string
	IPHash    string
	Timestamp time.Time
}

type Publisher interface {
	Publish(context.Context, ClickEvent) error
}

// queuedClick retains trace identity without retaining the request or its
// canceled context while publication happens in the background.
type queuedClick struct {
	event       ClickEvent
	spanContext trace.SpanContext
}

type Tracker struct {
	publisher Publisher
	clientIPs *ratelimit.ClientIPResolver
	salt      string
	logger    *slog.Logger
	queue     chan queuedClick
	closeOnce sync.Once
	waitGroup sync.WaitGroup
}

func NewTracker(publisher Publisher, clientIPs *ratelimit.ClientIPResolver, salt string, logger *slog.Logger, queueSize int) *Tracker {
	if queueSize <= 0 {
		queueSize = 1024
	}
	tracker := &Tracker{
		publisher: publisher,
		clientIPs: clientIPs,
		salt:      salt,
		logger:    logger.With(slog.String("component", "click_analytics")),
		queue:     make(chan queuedClick, queueSize),
	}
	tracker.waitGroup.Add(1)
	go tracker.run()
	return tracker
}

func (tracker *Tracker) Track(r *http.Request, code string) {
	event := ClickEvent{
		Code:      code,
		UserAgent: r.UserAgent(),
		Referer:   r.Referer(),
		IPHash:    tracker.hashIP(tracker.clientIPs.ClientIP(r)),
		Timestamp: time.Now().UTC(),
	}
	select {
	case tracker.queue <- queuedClick{event: event, spanContext: trace.SpanContextFromContext(r.Context())}:
	default:
		tracker.logger.WarnContext(r.Context(), "click analytics queue full; dropping event")
	}
}

func (tracker *Tracker) Close(ctx context.Context) error {
	tracker.closeOnce.Do(func() {
		close(tracker.queue)
	})
	done := make(chan struct{})
	go func() {
		tracker.waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (tracker *Tracker) run() {
	defer tracker.waitGroup.Done()
	for queued := range tracker.queue {
		parent := trace.ContextWithSpanContext(context.Background(), queued.spanContext)
		ctx, cancel := context.WithTimeout(parent, publishTimeout)
		err := tracker.publisher.Publish(ctx, queued.event)
		cancel()
		if err != nil {
			tracker.logger.ErrorContext(ctx, "failed to publish click analytics event", slog.Any("error", err))
		}
	}
}

func (tracker *Tracker) hashIP(ip string) string {
	digest := sha256.Sum256([]byte(ip + tracker.salt))
	return hex.EncodeToString(digest[:])
}
