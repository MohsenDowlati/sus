package logging

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

type requestKey struct{}

type requestState struct {
	mu       sync.Mutex
	span     trace.SpanContext
	cacheHit *bool
}

// ObserveRequest captures the server span created inside route middleware so
// the outer access logger can correlate its completion record with that span.
func ObserveRequest(ctx context.Context) {
	if state, ok := ctx.Value(requestKey{}).(*requestState); ok {
		state.mu.Lock()
		state.span = trace.SpanContextFromContext(ctx)
		state.mu.Unlock()
	}
}

// SetCacheHit annotates the request outcome without logging cache payloads.
func SetCacheHit(ctx context.Context, hit bool) {
	if state, ok := ctx.Value(requestKey{}).(*requestState); ok {
		state.mu.Lock()
		state.cacheHit = &hit
		state.mu.Unlock()
	}
}

// RequestLogger records a single access event after the handler and recovery
// finish. Duration is measured in fractional milliseconds without truncation.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			state := &requestState{span: trace.SpanContextFromContext(r.Context())}
			r = r.WithContext(context.WithValue(r.Context(), requestKey{}, state))
			wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				duration := time.Since(start)
				status := wrapped.Status()
				if status == 0 {
					status = http.StatusOK
				}
				// Routes cannot carry query tokens, credentials or raw client IPs.
				route := "unmatched"
				if routeCtx := chi.RouteContext(r.Context()); routeCtx != nil && routeCtx.RoutePattern() != "" {
					route = routeCtx.RoutePattern()
				}
				attrs := []slog.Attr{
					slog.String("method", r.Method), slog.String("route", route),
					slog.Int("status", status), slog.Int("bytes", wrapped.BytesWritten()),
					slog.Float64("duration_ms", float64(duration)/float64(time.Millisecond)),
				}
				if code := chi.URLParam(r, "code"); route == "/{code}" && code != "" {
					attrs = append(attrs, slog.String("code", code))
				}
				state.mu.Lock()
				sc := state.span
				if state.cacheHit != nil {
					attrs = append(attrs, slog.Bool("cache_hit", *state.cacheHit))
				}
				state.mu.Unlock()
				ctx := r.Context()
				if sc.IsValid() {
					ctx = trace.ContextWithSpanContext(ctx, sc)
				}
				message := "http_request"
				if route == "/{code}" {
					message = "link redirect completed"
					if status >= 300 && status < 400 {
						message = "link redirect executed"
					}
				}
				logger.LogAttrs(ctx, slog.LevelInfo, message, attrs...)
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

// Recoverer avoids writing unsanitized panic values and stack dumps to stderr.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				slog.Default().ErrorContext(r.Context(), "http handler panic",
					slog.String("panic_type", fmt.Sprintf("%T", recovered)))
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
