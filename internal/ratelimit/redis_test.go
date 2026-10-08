package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStore_SlidingWindowPreventsBoundaryBurst(t *testing.T) {
	store, cleanup := newRedisTestStore(t)
	defer cleanup()
	ctx := context.Background()
	key := "ratelimit:test:boundary"
	base := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

	for request := 0; request < 10; request++ {
		assertAllowed(t, store, ctx, key, 20, time.Minute, base)
	}
	for request := 0; request < 10; request++ {
		assertAllowed(t, store, ctx, key, 20, time.Minute, base.Add(30*time.Second))
	}

	decision, err := store.Allow(ctx, key, 20, time.Minute, base.Add(59*time.Second))
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("request at 59s was allowed, want sliding-window rejection")
	}

	for request := 0; request < 10; request++ {
		assertAllowed(t, store, ctx, key, 20, time.Minute, base.Add(60*time.Second+time.Millisecond))
	}
	decision, err = store.Allow(ctx, key, 20, time.Minute, base.Add(60*time.Second+time.Millisecond))
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("21st request in the rolling minute was allowed")
	}
	wantReset := base.Add(90 * time.Second)
	if !decision.ResetAt.Equal(wantReset) {
		t.Fatalf("ResetAt = %s, want %s", decision.ResetAt, wantReset)
	}
}

func TestRedisStore_ConcurrentBurstAllowsExactlyLimit(t *testing.T) {
	store, cleanup := newRedisTestStore(t)
	defer cleanup()
	const (
		callers = 100
		limit   = 20
	)
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	var allowed atomic.Int32
	var waitGroup sync.WaitGroup
	errorsByCaller := make(chan error, callers)

	for caller := 0; caller < callers; caller++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			decision, err := store.Allow(context.Background(), "ratelimit:test:burst", limit, time.Minute, now)
			if err != nil {
				errorsByCaller <- err
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		t.Fatalf("Allow() error = %v", err)
	}
	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed requests = %d, want %d", got, limit)
	}
}

func TestLimiterMiddleware_RealRedisScriptBlocksTwentyFirstRequest(t *testing.T) {
	store, cleanup := newRedisTestStore(t)
	defer cleanup()
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := New(store, logger)
	limiter.now = func() time.Time { return now }
	handler := limiter.Middleware(Policy{Name: "create-link", Limit: 20, Window: time.Minute, KeyFunc: IP})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		}),
	)

	for requestNumber := 1; requestNumber <= 21; requestNumber++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if requestNumber <= 20 && response.Code != http.StatusCreated {
			t.Fatalf("request %d status = %d, want %d", requestNumber, response.Code, http.StatusCreated)
		}
		if requestNumber == 21 {
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("request 21 status = %d, want %d", response.Code, http.StatusTooManyRequests)
			}
			if retryAfter := response.Header().Get("Retry-After"); retryAfter != "60" {
				t.Fatalf("Retry-After = %q, want 60", retryAfter)
			}
		}
	}
}

func assertAllowed(t *testing.T, store *RedisStore, ctx context.Context, key string, limit int64, window time.Duration, now time.Time) {
	t.Helper()
	decision, err := store.Allow(ctx, key, limit, window, now)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("request count %d was rejected", decision.Count)
	}
}

func newRedisTestStore(t *testing.T) (*RedisStore, func()) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	return NewRedisStore(client), func() {
		_ = client.Close()
		server.Close()
	}
}
