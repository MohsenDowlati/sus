package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/MohsenDowlati/shorts/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type WorkerConfig struct {
	Stream            string
	ConsumerGroup     string
	ConsumerName      string
	BatchSize         int64
	PollTimeout       time.Duration
	RetryMaxAttempts  int
	RetryBackoff      time.Duration
	PendingMinIdle    time.Duration
	ProcessingTimeout time.Duration
}

type Worker struct {
	source    StreamMessageSource
	processor BatchProcessor
	config    WorkerConfig
	logger    *slog.Logger
}

func NewWorker(source StreamMessageSource, processor BatchProcessor, config WorkerConfig, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		source:    source,
		processor: processor,
		config:    config,
		logger:    logger.With(slog.String("component", "analytics_worker")),
	}
}

func (worker *Worker) Run(ctx context.Context) error {
	tracer := otel.Tracer("shortener/analytics")
	startupCtx, startupSpan := tracer.Start(ctx, "analytics.worker.start")
	if err := worker.retry(startupCtx, func(attemptCtx context.Context) error {
		return worker.source.EnsureGroup(attemptCtx, worker.config.Stream, worker.config.ConsumerGroup)
	}); err != nil {
		telemetry.RecordError(startupCtx, err)
		startupSpan.End()
		return fmt.Errorf("ensure consumer group: %w", err)
	}

	worker.logger.InfoContext(startupCtx, "analytics worker started",
		slog.String("stream", worker.config.Stream),
		slog.String("group", worker.config.ConsumerGroup),
		slog.String("consumer", worker.config.ConsumerName),
		slog.Int64("batch_size", worker.config.BatchSize),
	)

	startupSpan.End()
	worker.recoverOwnedPending(ctx)
	claimStart := "0-0"

	for ctx.Err() == nil {
		claimCtx, claimSpan := tracer.Start(ctx, "analytics.claim")
		claimed, next, err := worker.source.Claim(
			claimCtx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			worker.config.PendingMinIdle,
			claimStart,
			worker.config.BatchSize,
		)
		telemetry.RecordError(claimCtx, err)
		claimSpan.End()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			worker.logger.ErrorContext(claimCtx, "failed to claim pending messages", slog.Any("error", err))
			if !worker.wait(ctx, worker.config.RetryBackoff) {
				break
			}
		} else {
			claimStart = next
			if claimStart == "" {
				claimStart = "0-0"
			}
			if len(claimed) > 0 {
				worker.handleBatch(claimCtx, claimed)
				continue
			}
		}
		if ctx.Err() != nil {
			break
		}

		readCtx, readSpan := tracer.Start(ctx, "analytics.read")
		messages, err := worker.source.Read(
			readCtx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			">",
			worker.config.BatchSize,
			worker.config.PollTimeout,
		)
		telemetry.RecordError(readCtx, err)
		readSpan.End()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			worker.logger.ErrorContext(readCtx, "failed to read click events", slog.Any("error", err))
			if !worker.wait(ctx, worker.config.RetryBackoff) {
				break
			}
			continue
		}
		if len(messages) > 0 {
			worker.handleBatch(readCtx, messages)
		}
		if ctx.Err() != nil {
			break
		}
	}

	stopCtx, stopSpan := tracer.Start(context.WithoutCancel(ctx), "analytics.worker.stop")
	worker.logger.InfoContext(stopCtx, "analytics worker stopped")
	stopSpan.End()
	return nil
}

func (worker *Worker) recoverOwnedPending(ctx context.Context) {
	for ctx.Err() == nil {
		readCtx, readSpan := otel.Tracer("shortener/analytics").Start(ctx, "analytics.read_pending")
		messages, err := worker.source.Read(
			readCtx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			"0",
			worker.config.BatchSize,
			-1,
		)
		telemetry.RecordError(readCtx, err)
		readSpan.End()
		if err != nil {
			worker.logger.ErrorContext(readCtx, "failed to read consumer pending messages", slog.Any("error", err))
			return
		}
		if len(messages) == 0 {
			return
		}
		if !worker.handleBatch(readCtx, messages) {
			return
		}
	}
}

func (worker *Worker) handleBatch(parent context.Context, messages []redis.XMessage) bool {
	processCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), worker.config.ProcessingTimeout)
	defer cancel()
	processCtx, span := otel.Tracer("shortener/analytics").Start(processCtx, "analytics.process_batch",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.Int("messaging.batch.message_count", len(messages))))
	defer span.End()
	start := time.Now()

	if err := worker.retry(processCtx, func(attemptCtx context.Context) error {
		return worker.processor.Process(attemptCtx, messages)
	}); err != nil {
		telemetry.RecordError(processCtx, err)
		worker.logger.ErrorContext(processCtx, "failed to process click event batch",
			slog.Int("count", len(messages)),
			slog.Any("error", err),
		)
		return false
	}

	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	if err := worker.retry(processCtx, func(attemptCtx context.Context) error {
		return worker.source.Ack(attemptCtx, worker.config.Stream, worker.config.ConsumerGroup, ids...)
	}); err != nil {
		telemetry.RecordError(processCtx, err)
		worker.logger.ErrorContext(processCtx, "failed to acknowledge click event batch",
			slog.Int("count", len(messages)),
			slog.Any("error", err),
		)
		return false
	}

	worker.logger.DebugContext(processCtx, "processed click event batch", slog.Int("count", len(messages)),
		slog.Float64("duration_ms", float64(time.Since(start))/float64(time.Millisecond)))
	return true
}

func (worker *Worker) retry(ctx context.Context, operation func(context.Context) error) error {
	var err error
	for attempt := 1; attempt <= worker.config.RetryMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = operation(ctx); err == nil {
			return nil
		}
		if attempt < worker.config.RetryMaxAttempts && !worker.wait(ctx, worker.config.RetryBackoff) {
			return ctx.Err()
		}
	}
	return err
}

func (worker *Worker) wait(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
