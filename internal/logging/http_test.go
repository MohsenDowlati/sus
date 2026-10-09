package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/logging"
	"github.com/MohsenDowlati/shorts/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestAccessRecordUsesServerSpanAndFractionalMilliseconds(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	var output bytes.Buffer
	logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil)))
	router := chi.NewRouter()
	router.Use(logging.RequestLogger(logger))
	router.Use(logging.Recoverer)
	var elapsed time.Duration
	router.With(telemetry.HTTP("GET /{code}")).Get("/{code}", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		logging.SetCacheHit(r.Context(), true)
		w.WriteHeader(http.StatusTemporaryRedirect)
		elapsed = time.Since(start)
	})
	request := httptest.NewRequest(http.MethodGet, "/my-slug?api_key=private-query-token", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("Authorization", "Bearer private-header-token")
	request.Header.Set("X-Request-ID", "private-request-id-token")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	router.ServeHTTP(httptest.NewRecorder(), request)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil { t.Fatal(err) }
	if record["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || record["span_id"] == "00f067aa0ba902b7" || record["span_id"] == nil {
		t.Fatal("access log must use the HTTP server span, not its remote parent")
	}
	if record["status"] != float64(307) || record["route"] != "/{code}" || record["code"] != "my-slug" || record["cache_hit"] != true {
		t.Fatalf("unexpected access record: %v", record)
	}
	milliseconds, ok := record["duration_ms"].(float64)
	if !ok || milliseconds < float64(elapsed)/float64(time.Millisecond) { t.Fatal("duration must include handler execution without millisecond truncation") }
	if _, ok := record["duration"]; ok { t.Fatal("access log should use duration_ms") }
	for _, secret := range []string{"198.51.100.20", "private-query-token", "private-header-token", "private-request-id-token"} {
		if strings.Contains(output.String(), secret) { t.Fatalf("access log leaked %s", secret) }
	}
}

func TestAccessRecordCapturesImplicitOKAndRecoveredPanic(t *testing.T) {
	previous := slog.Default()
	for _, tc := range []struct { name string; panic bool; status int }{{"implicit success", false, 200}, {"recovered panic", true, 500}} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil)))
			slog.SetDefault(logger)
			defer slog.SetDefault(previous)
			handler := logging.RequestLogger(logger)(logging.Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				if tc.panic { panic("password=panic-secret client=198.51.100.20") }
			})))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			decoder := json.NewDecoder(strings.NewReader(output.String()))
			var record map[string]any
			for decoder.More() {
				if err := decoder.Decode(&record); err != nil { t.Fatal(err) }
			}
			if record["status"] != float64(tc.status) { t.Fatalf("status = %v", record["status"]) }
			if strings.Contains(output.String(), "panic-secret") { t.Fatal("panic leaked secret") }
		})
	}
}
