package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPContinuesRemoteParentAndStartsNewTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	var received trace.SpanContext
	handler := HTTP("GET /{code}")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	request := httptest.NewRequest(http.MethodGet, "/abc123", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want one server span", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /{code}" || span.SpanKind != trace.SpanKindServer {
		t.Fatalf("unexpected server span: %s, %v", span.Name, span.SpanKind)
	}
	if span.SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		span.Parent.SpanID().String() != "00f067aa0ba902b7" || !span.Parent.IsRemote() {
		t.Fatal("server span did not continue the remote W3C parent")
	}
	if !received.Equal(span.SpanContext) {
		t.Fatal("handler did not receive the server span context")
	}

	exporter.Reset()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/other-code", nil))
	spans = exporter.GetSpans()
	if len(spans) != 1 || !spans[0].SpanContext.IsValid() || spans[0].Parent.IsValid() {
		t.Fatal("request without traceparent did not start a new root span")
	}
}

func TestInitRejectsUnsupportedProtocol(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "invalid")
	provider, err := Init(context.Background(), "shortener-test")
	if err == nil || provider != nil {
		t.Fatal("unsupported protocol must fail startup")
	}
}


func TestHTTPFailureRecordsStatusEventAndErrorCode(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	handler := HTTP("GET /{code}")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error || len(spans[0].Events) == 0 { t.Fatal("HTTP 404 missing error status or event") }
	for _, attr := range spans[0].Attributes { if string(attr.Key) == "error.code" && attr.Value.AsString() == "404" { return } }
	t.Fatal("HTTP response code missing")
}
