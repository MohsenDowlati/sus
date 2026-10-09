package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MohsenDowlati/shorts/internal/metrics"
	"github.com/MohsenDowlati/shorts/internal/ratelimit"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func TestMetricsRouteProvidesValidExpositionWithBoundedDimensions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(nil, nil, nil, ratelimit.New(nil, logger), logger)
	metrics.Requests.WithLabelValues("auto", "201").Inc()
	metrics.DatabaseOperation("find", "links")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics response = %d, %s", response.Code, response.Header().Get("Content-Type"))
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(response.Body.String()))
	if err != nil { t.Fatalf("invalid Prometheus exposition: %v", err) }
	allowed := map[string]map[string]bool{
		"shortener_requests_total": {"type": true, "status": true},
		"shortener_redirect_latency_seconds": {},
		"shortener_cache_hits_total": {},
		"shortener_cache_misses_total": {},
		"shortener_db_operations_total": {"operation": true, "collection": true},
	}
	for name, dimensions := range allowed {
		family := families[name]
		if family == nil { t.Fatalf("missing metric %s", name) }
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !dimensions[label.GetName()] { t.Fatalf("unexpected label %s on %s", label.GetName(), name) }
			}
		}
	}
}
