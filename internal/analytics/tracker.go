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

type Tracker struct {
	publisher Publisher
	clientIPs *ratelimit.ClientIPResolver
	salt      string
	logger    *slog.Logger
	queue     chan ClickEvent
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
		queue:     make(chan ClickEvent, queueSize),
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
	case tracker.queue <- event:
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
	for event := range tracker.queue {
		ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
		err := tracker.publisher.Publish(ctx, event)
		cancel()
		if err != nil {
			tracker.logger.Error("failed to publish click analytics event", slog.Any("error", err))
		}
	}
}

func (tracker *Tracker) hashIP(ip string) string {
	digest := sha256.Sum256([]byte(ip + tracker.salt))
	return hex.EncodeToString(digest[:])
}
