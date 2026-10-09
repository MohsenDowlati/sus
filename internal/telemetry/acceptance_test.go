package telemetry_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/analytics"
	"github.com/MohsenDowlati/shorts/internal/ratelimit"
	"github.com/MohsenDowlati/shorts/internal/telemetry"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func tracingFixture(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	return exporter
}

func tracedRedis(t *testing.T) *redis.Client {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	if err := redisotel.InstrumentTracing(client); err != nil { t.Fatal(err) }
	client.AddHook(telemetry.RedisErrorHook{})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedirectTraceParentsCacheMissMongoAndAsyncXADD(t *testing.T) {
	exporter := tracingFixture(t)
	client := tracedRedis(t)
	monitor := telemetry.MongoMonitor()
	resolver, _ := ratelimit.NewClientIPResolver(nil)
	tracker := analytics.NewTracker(analytics.NewRedisStreamPublisher(client), resolver, "test-salt", slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	command, err := bson.Marshal(bson.D{{Key:"find", Value:"links"}, {Key:"filter", Value:bson.M{"code":"missing-slug"}}})
	if err != nil { t.Fatal(err) }
	handler := telemetry.HTTP("GET /{code}")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := client.Get(r.Context(), "link:missing-slug").Err(); err != redis.Nil { t.Errorf("expected cache miss, got %v", err) }
		// Driver callbacks exercise the real Mongo monitor without a Mongo server.
		monitor.Started(r.Context(), &event.CommandStartedEvent{Command:command, CommandName:"find", DatabaseName:"shorts", RequestID:1, ConnectionID:"test-connection"})
		monitor.Succeeded(r.Context(), &event.CommandSucceededEvent{CommandFinishedEvent:event.CommandFinishedEvent{CommandName:"find", RequestID:1, ConnectionID:"test-connection"}})
		if err := client.Set(r.Context(), "link:missing-slug", "cached", time.Minute).Err(); err != nil { t.Error(err) }
		tracker.Track(r, "missing-slug")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing-slug", nil))
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tracker.Close(closeCtx); err != nil { t.Fatal(err) }
	spans := exporter.GetSpans()
	var server trace.SpanContext
	for _, span := range spans { if span.Name == "GET /{code}" { server = span.SpanContext } }
	if !server.IsValid() { t.Fatal("missing server span") }
	wanted := map[string]bool{"get":false, "links.find":false, "set":false, "xadd":false}
	for _, span := range spans {
		if _, ok := wanted[span.Name]; !ok { continue }
		if span.Parent.SpanID() != server.SpanID() || span.SpanContext.TraceID() != server.TraceID() { t.Fatalf("%s lost the HTTP parent", span.Name) }
		if span.Status.Code == codes.Error { t.Fatalf("successful %s or cache miss marked as error", span.Name) }
		wanted[span.Name] = true
	}
	for name, found := range wanted { if !found { t.Fatalf("missing %s span", name) } }
}

func TestRedisProtocolErrorHasStatusEventAndCode(t *testing.T) {
	exporter := tracingFixture(t)
	client := tracedRedis(t)
	if err := client.RPush(context.Background(), "link:wrong-type", "value").Err(); err != nil { t.Fatal(err) }
	if err := client.Get(context.Background(), "link:wrong-type").Err(); err == nil { t.Fatal("expected WRONGTYPE") }
	for _, span := range exporter.GetSpans() {
		if span.Name != "get" { continue }
		if span.Status.Code != codes.Error || len(span.Events) == 0 { t.Fatal("Redis error missing status or exception") }
		for _, attr := range span.Attributes { if string(attr.Key) == "error.code" && attr.Value.AsString() == "WRONGTYPE" { return } }
		t.Fatal("Redis error missing protocol code")
	}
	t.Fatal("missing Redis GET span")
}

func TestMongoWriteErrorReplyMarksCommandSpan(t *testing.T) {
	exporter := tracingFixture(t)
	monitor := telemetry.MongoMonitor()
	command, _ := bson.Marshal(bson.D{{Key:"insert", Value:"links"}})
	reply, _ := bson.Marshal(bson.M{"ok":1, "writeErrors":bson.A{bson.M{"code":int32(11000), "errmsg":"duplicate key"}}})
	monitor.Started(context.Background(), &event.CommandStartedEvent{Command:command, CommandName:"insert", DatabaseName:"shorts", RequestID:2, ConnectionID:"test-connection"})
	monitor.Succeeded(context.Background(), &event.CommandSucceededEvent{Reply:reply, CommandFinishedEvent:event.CommandFinishedEvent{RequestID:2, ConnectionID:"test-connection"}})
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error || len(spans[0].Events) == 0 { t.Fatal("MongoDB write error did not mark command span") }
	for _, attr := range spans[0].Attributes { if string(attr.Key) == "db.response.status_code" && attr.Value.AsString() == "11000" { return } }
	t.Fatal("MongoDB numeric code missing")
}
