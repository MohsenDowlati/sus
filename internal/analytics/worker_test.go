package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/logging"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeMessageSource struct {
	ensureCalls int
	readFunc    func(string) ([]redis.XMessage, error)
	claimFunc   func() ([]redis.XMessage, string, error)
	acked       []string
}

func (source *fakeMessageSource) EnsureGroup(context.Context, string, string) error {
	source.ensureCalls++
	return nil
}

func (source *fakeMessageSource) Read(_ context.Context, _, _, _, start string, _ int64, _ time.Duration) ([]redis.XMessage, error) {
	return source.readFunc(start)
}

func (source *fakeMessageSource) Claim(context.Context, string, string, string, time.Duration, string, int64) ([]redis.XMessage, string, error) {
	if source.claimFunc == nil {
		return nil, "0-0", nil
	}
	return source.claimFunc()
}

func (source *fakeMessageSource) Ack(_ context.Context, _, _ string, ids ...string) error {
	source.acked = append(source.acked, ids...)
	return nil
}

type fakeBatchProcessor struct {
	err      error
	calls    int
	messages [][]redis.XMessage
}

func (processor *fakeBatchProcessor) Process(_ context.Context, messages []redis.XMessage) error {
	processor.calls++
	processor.messages = append(processor.messages, messages)
	return processor.err
}

func TestWorkerFinishesAndAcknowledgesCurrentBatchAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	message := redis.XMessage{ID: "1-0"}
	reads := 0
	source := &fakeMessageSource{}
	source.readFunc = func(start string) ([]redis.XMessage, error) {
		if start == "0" {
			return nil, nil
		}
		reads++
		cancel()
		return []redis.XMessage{message}, nil
	}
	processor := &fakeBatchProcessor{}

	worker := NewWorker(source, processor, testWorkerConfig(), discardLogger())
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if reads != 1 {
		t.Fatalf("new-message reads = %d, want 1", reads)
	}
	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if !reflect.DeepEqual(source.acked, []string{"1-0"}) {
		t.Fatalf("acked IDs = %v, want [1-0]", source.acked)
	}
}

func TestWorkerDoesNotAcknowledgeFailedBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeMessageSource{}
	source.readFunc = func(start string) ([]redis.XMessage, error) {
		if start == "0" {
			return nil, nil
		}
		cancel()
		return []redis.XMessage{{ID: "2-0"}}, nil
	}
	processor := &fakeBatchProcessor{err: errors.New("mongo unavailable")}
	config := testWorkerConfig()
	config.RetryMaxAttempts = 3

	worker := NewWorker(source, processor, config, discardLogger())
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if processor.calls != 3 {
		t.Fatalf("processor calls = %d, want 3", processor.calls)
	}
	if len(source.acked) != 0 {
		t.Fatalf("acked IDs = %v, want none", source.acked)
	}
}

func TestWorkerProcessesOwnedPendingBeforeNewMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	source := &fakeMessageSource{}
	source.readFunc = func(start string) ([]redis.XMessage, error) {
		reads++
		if reads == 1 && start == "0" {
			return []redis.XMessage{{ID: "3-0"}}, nil
		}
		cancel()
		return nil, nil
	}
	processor := &fakeBatchProcessor{}

	worker := NewWorker(source, processor, testWorkerConfig(), discardLogger())
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if !reflect.DeepEqual(source.acked, []string{"3-0"}) {
		t.Fatalf("acked IDs = %v, want [3-0]", source.acked)
	}
}

func testWorkerConfig() WorkerConfig {
	return WorkerConfig{
		Stream:            ClickStream,
		ConsumerGroup:     "test-group",
		ConsumerName:      "test-consumer",
		BatchSize:         10,
		PollTimeout:       time.Millisecond,
		RetryMaxAttempts:  1,
		RetryBackoff:      0,
		PendingMinIdle:    time.Second,
		ProcessingTimeout: time.Second,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}


func TestWorkerBatchLogsMatchActiveSpanAndPreserveParentAfterCancellation(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure { name = "database failure" }
		t.Run(name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
			parentCtx, parent := provider.Tracer("test").Start(context.Background(), "test.parent")
			defer parent.End()
			parentCtx, cancel := context.WithCancel(parentCtx)
			cancel()
			var logs bytes.Buffer
			logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level:slog.LevelDebug})))
			source := &fakeMessageSource{}
			processor := &fakeBatchProcessor{}
			if failure { processor.err = mongo.CommandError{Code:13, Message:"unauthorized"} }
			worker := NewWorker(source, processor, testWorkerConfig(), logger)
			if got := worker.handleBatch(parentCtx, []redis.XMessage{{ID:"1-0"}}); got == failure { t.Fatal("unexpected batch outcome") }
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil { t.Fatalf("invalid JSON log: %v", err) }
			spans := exporter.GetSpans()
			if len(spans) != 1 || spans[0].Name != "analytics.process_batch" { t.Fatal("missing processing span") }
			span := spans[0]
			if record["trace_id"] != span.SpanContext.TraceID().String() || record["span_id"] != span.SpanContext.SpanID().String() || !span.Parent.Equal(parent.SpanContext()) {
				t.Fatal("batch log or span lost its parent trace context")
			}
			if failure {
				if span.Status.Code != codes.Error || len(span.Events) == 0 { t.Fatal("failed batch missing error status or event") }
				for _, attr := range span.Attributes { if string(attr.Key) == "db.response.status_code" && attr.Value.AsString() == "13" { return } }
				t.Fatal("MongoDB failure code missing")
			}
			if _, ok := record["duration_ms"].(float64); !ok { t.Fatal("batch duration must be numeric milliseconds") }
		})
	}
}
