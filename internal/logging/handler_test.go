package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func logContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil { t.Fatal(err) }
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil { t.Fatal(err) }
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID,
	}))
}

func TestHandlerCorrelatesGroupedRecordsAndSanitizesBoundValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).
		With("component", "shortener", "access_token", "opaque-api-token").
		WithGroup("operation").With("code", "my-slug")
	logger.InfoContext(logContext(t), "link redirect executed", "status", 307,
		"trace_id", "spoofed-trace", "span_id", "spoofed-span",
		"credentials", slog.GroupValue(slog.String("password", "hidden-password")))
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil { t.Fatal(err) }
	if record["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || record["span_id"] != "00f067aa0ba902b7" {
		t.Fatal("trace fields must reflect the active span at the root")
	}
	group, ok := record["operation"].(map[string]any)
	if !ok || group["code"] != "my-slug" || group["status"] != float64(307) { t.Fatal("derived logger lost grouped attributes") }
	for _, secret := range []string{"opaque-api-token", "hidden-password", "spoofed-trace", "spoofed-span"} {
		if strings.Contains(output.String(), secret) { t.Fatalf("log leaked %s", secret) }
	}
}

func TestHandlerRedactsSensitiveStringsAndOpaqueObjects(t *testing.T) {
	t.Setenv("DB_PASS", "configured-database-password")
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil)))
	logger.Error("failed on 198.51.100.20 and [2001:db8::1] Bearer sensitive-bearer-value configured-database-password",
		"endpoint", "mongodb://private-user:private-password@localhost/db?api_key=private-key",
		"note", "Basic dXNlcjpwYXNz eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature",
		"error", errors.New("driver leaked password=driver-secret 192.0.2.15"),
		"details", map[string]string{"opaque": "opaque-secret"},
		"client_ip", "203.0.113.10",
		"nested", slog.GroupValue(slog.String("refresh-token", "private-refresh")))
	for _, secret := range []string{"198.51.100.20", "2001:db8::1", "sensitive-bearer-value", "configured-database-password", "private-user", "private-password", "private-key", "dXNlcjpwYXNz", "eyJhbGciOiJIUzI1NiJ9", "driver-secret", "192.0.2.15", "opaque-secret", "203.0.113.10", "private-refresh"} {
		if strings.Contains(output.String(), secret) { t.Fatalf("log leaked %s: %s", secret, output.String()) }
	}
}

func TestHandlerOmitsTraceFieldsWithoutAnActiveContext(t *testing.T) {
	var output bytes.Buffer
	slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).Info("startup", "trace_id", "spoofed")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil { t.Fatal(err) }
	if _, ok := record["trace_id"]; ok { t.Fatal("startup record must not have a fabricated trace ID") }
	if _, ok := record["span_id"]; ok { t.Fatal("startup record must not have a fabricated span ID") }
}
