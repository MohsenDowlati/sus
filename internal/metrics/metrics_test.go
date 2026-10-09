package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func counterValue(c prometheus.Counter) float64 {
	m := &dto.Metric{}
	_ = c.Write(m)
	return m.GetCounter().GetValue()
}

func TestRequestRecordsActualStatusAndReplaysBody(t *testing.T) {
	for _, tc := range []struct {
		body, kind, label, status string
		code                      int
	}{
		{`{"type":"custom","original_url":"https://example.com"}`, "create", "custom", "201", 201},
		{`{"type":"custom"}`, "create", "custom", "429", 429},
		{`{"type":"auto"}`, "create", "auto", "409", 409},
		{`{`, "create", "auto", "400", 400},
		{"", "redirect", "redirect", "307", 307},
		{"", "redirect", "redirect", "404", 404},
		{"", "redirect", "redirect", "410", 410},
	} {
		t.Run(tc.label+tc.status, func(t *testing.T) {
			counter := Requests.WithLabelValues(tc.label, tc.status)
			before := counterValue(counter)
			handler := Request(tc.kind)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != tc.body {
					t.Errorf("replayed body = %q, err = %v", body, err)
				}
				w.WriteHeader(tc.code)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(tc.body)))
			if response.Code != tc.code || counterValue(counter) != before+1 {
				t.Fatal("request did not record actual HTTP status exactly once")
			}
		})
	}
}

func TestRequestRecordsRecoveredPanic(t *testing.T) {
	counter := Requests.WithLabelValues("redirect", "500")
	before := counterValue(counter)
	handler := Request("redirect")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("failed lookup") }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/missing", nil))
	if response.Code != 500 || counterValue(counter) != before+1 {
		t.Fatal("panic not recorded as HTTP 500")
	}
}

func TestHistogramBucketsAndExposition(t *testing.T) {
	m := &dto.Metric{}
	if err := RedirectLatency.(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	want := []float64{0.0001, 0.00025, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}
	buckets := m.GetHistogram().GetBucket()
	if len(buckets) != len(want) {
		t.Fatalf("got %d buckets", len(buckets))
	}
	for i, bound := range want {
		if buckets[i].GetUpperBound() != bound {
			t.Errorf("bucket %d differs", i)
		}
	}
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "shortener_redirect_latency_seconds_bucket") {
		t.Fatal("missing Prometheus histogram exposition")
	}
}
