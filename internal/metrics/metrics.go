// Package metrics exposes the shortener's Prometheus instrumentation.
package metrics

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/MohsenDowlati/shorts/internal/logging"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Requests        = promauto.NewCounterVec(prometheus.CounterOpts{Name: "shortener_requests_total", Help: "Completed link creation and redirect requests by type and HTTP status."}, []string{"type", "status"})
	RedirectLatency = promauto.NewHistogram(prometheus.HistogramOpts{Name: "shortener_redirect_latency_seconds", Help: "Redirect request duration in seconds, including failed lookups.", Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}})
	CacheHits       = promauto.NewCounter(prometheus.CounterOpts{Name: "shortener_cache_hits_total", Help: "Redis link cache lookups returning a value, including negative cache entries."})
	CacheMisses     = promauto.NewCounter(prometheus.CounterOpts{Name: "shortener_cache_misses_total", Help: "Redis link cache lookups with no entry. Redis errors are excluded."})
	DBOperations    = promauto.NewCounterVec(prometheus.CounterOpts{Name: "shortener_db_operations_total", Help: "Attempted database operations, including failures and retries. Bulk writes count once per operation kind."}, []string{"operation", "collection"})
)

func Handler() http.Handler { return promhttp.Handler() }

// Request instruments one route outside its authentication and rate limiting middleware.
func Request(kind string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestType := kind
			if kind == "create" {
				requestType = "auto"
				// Inspect a bounded prefix and replay it for the handler. Preserve body closure.
				if r.Body != nil {
					body := r.Body
					prefix, _ := io.ReadAll(io.LimitReader(body, 64<<10))
					r.Body = struct {
						io.Reader
						io.Closer
					}{io.MultiReader(bytes.NewReader(prefix), body), body}
					var payload struct {
						Type string `json:"type"`
					}
					if json.Unmarshal(prefix, &payload) == nil && payload.Type == "custom" {
						requestType = "custom"
					}
				}
			}
			start := time.Now()
			wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := wrapped.Status()
				if status == 0 {
					status = http.StatusOK
				}
				Requests.WithLabelValues(requestType, strconv.Itoa(status)).Inc()
				if kind == "redirect" {
					RedirectLatency.Observe(time.Since(start).Seconds())
				}
			}()
			// Recovery must write through the recorder so panics count as HTTP 500.
			logging.Recoverer(next).ServeHTTP(wrapped, r)
		})
	}
}

// DatabaseOperation restricts labels to the requested shortener collections.
func DatabaseOperation(operation, collection string) {
	switch operation {
	case "insert", "find", "update":
	default:
		return
	}
	if collection == "links" || collection == "click_events" {
		DBOperations.WithLabelValues(operation, collection).Inc()
	}
}
