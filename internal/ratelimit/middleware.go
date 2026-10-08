package ratelimit

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
)

type KeyFunc func(*http.Request) string

type Policy struct {
	Name    string
	Limit   int64
	Window  time.Duration
	KeyFunc KeyFunc
}

type Limiter struct {
	store     Store
	logger    *slog.Logger
	now       func() time.Time
	clientIPs *ClientIPResolver
}

func New(store Store, logger *slog.Logger) *Limiter {
	resolver, _ := NewClientIPResolver(nil)
	return NewWithClientIPResolver(store, logger, resolver)
}

func NewWithClientIPResolver(store Store, logger *slog.Logger, resolver *ClientIPResolver) *Limiter {
	return &Limiter{
		store:     store,
		logger:    logger.With(slog.String("component", "rate_limiter")),
		now:       time.Now,
		clientIPs: resolver,
	}
}

func (limiter *Limiter) Middleware(policy Policy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := policy.KeyFunc(r)
			key := "ratelimit:" + policy.Name + ":" + identity
			now := limiter.now().UTC()
			decision, err := limiter.store.Allow(r.Context(), key, policy.Limit, policy.Window, now)
			if err != nil {
				limiter.logger.ErrorContext(r.Context(), "rate limit check failed; allowing request",
					slog.String("key", key),
					slog.Any("error", err),
				)
				next.ServeHTTP(w, r)
				return
			}

			setHeaders(w.Header(), policy.Limit, decision, now)
			if !decision.Allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func UserOrIP(r *http.Request) string {
	if userID := auth.UserID(r.Context()); userID != "" {
		return "user:" + userID
	}
	return "ip:" + ClientIP(r)
}

func (limiter *Limiter) UserOrIP(r *http.Request) string {
	if userID := auth.UserID(r.Context()); userID != "" {
		return "user:" + userID
	}
	return "ip:" + limiter.clientIPs.ClientIP(r)
}

func (limiter *Limiter) IP(r *http.Request) string {
	return "ip:" + limiter.clientIPs.ClientIP(r)
}

func IP(r *http.Request) string {
	return "ip:" + ClientIP(r)
}

func ClientIP(r *http.Request) string {
	resolver, _ := NewClientIPResolver(nil)
	return resolver.ClientIP(r)
}

func setHeaders(header http.Header, limit int64, decision Decision, now time.Time) {
	remaining := limit - decision.Count
	if remaining < 0 {
		remaining = 0
	}
	header.Set("X-RateLimit-Limit", strconv.FormatInt(limit, 10))
	header.Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	header.Set("X-RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
	if !decision.Allowed {
		retryAfter := int64((decision.ResetAt.Sub(now) + time.Second - 1) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		header.Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	}
}
