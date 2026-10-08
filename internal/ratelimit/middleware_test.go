package ratelimit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
)

type fakeStore struct {
	decision Decision
	err      error
	key      string
	limit    int64
	window   time.Duration
}

func (store *fakeStore) Allow(_ context.Context, key string, limit int64, window time.Duration, _ time.Time) (Decision, error) {
	store.key = key
	store.limit = limit
	store.window = window
	return store.decision, store.err
}

func TestLimiterMiddleware_SetsHeadersForAllowedRequest(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{decision: Decision{Allowed: true, Count: 6, ResetAt: now.Add(45 * time.Second)}}
	limiter := newTestLimiter(store, now)
	handler := limiter.Middleware(Policy{Name: "create-link", Limit: 20, Window: time.Minute, KeyFunc: IP})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	assertHeader(t, response, "X-RateLimit-Limit", "20")
	assertHeader(t, response, "X-RateLimit-Remaining", "14")
	assertHeader(t, response, "X-RateLimit-Reset", strconv.FormatInt(now.Add(45*time.Second).Unix(), 10))
	if store.key != "ratelimit:create-link:ip:203.0.113.10" {
		t.Fatalf("rate limit key = %q", store.key)
	}
}

func TestLimiterMiddleware_RejectsWithRetryAfter(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{decision: Decision{Allowed: false, Count: 20, ResetAt: now.Add(21 * time.Second)}}
	limiter := newTestLimiter(store, now)
	handler := limiter.Middleware(Policy{Name: "create-link", Limit: 20, Window: time.Minute, KeyFunc: IP})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler must not run after rate limit rejection")
		}),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	assertHeader(t, response, "X-RateLimit-Limit", "20")
	assertHeader(t, response, "X-RateLimit-Remaining", "0")
	assertHeader(t, response, "Retry-After", "21")
}

func TestLimiterMiddleware_FailsOpenWhenRedisFails(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{err: errors.New("Redis unavailable")}
	limiter := newTestLimiter(store, now)
	handler := limiter.Middleware(Policy{Name: "check-slug", Limit: 60, Window: time.Minute, KeyFunc: IP})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/links/check-slug", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want fail-open status %d", response.Code, http.StatusNoContent)
	}
}

func TestUserOrIP_UsesAuthenticatedUserID(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{decision: Decision{Allowed: true, Count: 1, ResetAt: now.Add(time.Minute)}}
	limiter := newTestLimiter(store, now)
	tokens := auth.NewTokenService(
		"access-secret-used-only-for-this-unit-test",
		"refresh-secret-used-only-for-this-unit-test",
		time.Hour,
		time.Hour,
	)
	token, err := tokens.GenerateAccess("user-123", "test-user")
	if err != nil {
		t.Fatalf("GenerateAccess() error = %v", err)
	}
	next := limiter.Middleware(Policy{Name: "create-link", Limit: 20, Window: time.Minute, KeyFunc: limiter.UserOrIP})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)
	handler := auth.Authenticator(tokens)(next)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if store.key != "ratelimit:create-link:user:user-123" {
		t.Fatalf("rate limit key = %q, want authenticated user key", store.key)
	}
}

func TestLimiterUserOrIP_FallsBackToCanonicalForwardedClient(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{decision: Decision{Allowed: true, Count: 1, ResetAt: now.Add(time.Minute)}}
	clientIPs, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := NewWithClientIPResolver(store, logger, clientIPs)
	limiter.now = func() time.Time { return now }
	handler := limiter.Middleware(Policy{Name: "create-link", Limit: 20, Window: time.Minute, KeyFunc: limiter.UserOrIP})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "::ffff:198.51.100.20")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if store.key != "ratelimit:create-link:ip:198.51.100.20" {
		t.Fatalf("rate limit key = %q, want canonical forwarded IP key", store.key)
	}
}

func newTestLimiter(store Store, now time.Time) *Limiter {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := New(store, logger)
	limiter.now = func() time.Time { return now }
	return limiter
}

func assertHeader(t *testing.T, response *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	if got := response.Header().Get(name); got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
