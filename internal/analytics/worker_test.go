package analytics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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
