package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
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
	if err := worker.retry(ctx, func(attemptCtx context.Context) error {
		return worker.source.EnsureGroup(attemptCtx, worker.config.Stream, worker.config.ConsumerGroup)
	}); err != nil {
		return fmt.Errorf("ensure consumer group: %w", err)
	}

	worker.logger.Info("analytics worker started",
		slog.String("stream", worker.config.Stream),
		slog.String("group", worker.config.ConsumerGroup),
		slog.String("consumer", worker.config.ConsumerName),
		slog.Int64("batch_size", worker.config.BatchSize),
	)

	worker.recoverOwnedPending(ctx)
	claimStart := "0-0"

	for ctx.Err() == nil {
		claimed, next, err := worker.source.Claim(
			ctx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			worker.config.PendingMinIdle,
			claimStart,
			worker.config.BatchSize,
		)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			worker.logger.Error("failed to claim pending messages", slog.Any("error", err))
			if !worker.wait(ctx, worker.config.RetryBackoff) {
				break
			}
		} else {
			claimStart = next
			if claimStart == "" {
				claimStart = "0-0"
			}
			if len(claimed) > 0 {
				worker.handleBatch(claimed)
				continue
			}
		}
		if ctx.Err() != nil {
			break
		}

		messages, err := worker.source.Read(
			ctx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			">",
			worker.config.BatchSize,
			worker.config.PollTimeout,
		)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			worker.logger.Error("failed to read click events", slog.Any("error", err))
			if !worker.wait(ctx, worker.config.RetryBackoff) {
				break
			}
			continue
		}
		if len(messages) > 0 {
			worker.handleBatch(messages)
		}
		if ctx.Err() != nil {
			break
		}
	}

	worker.logger.Info("analytics worker stopped")
	return nil
}

func (worker *Worker) recoverOwnedPending(ctx context.Context) {
	for ctx.Err() == nil {
		messages, err := worker.source.Read(
			ctx,
			worker.config.Stream,
			worker.config.ConsumerGroup,
			worker.config.ConsumerName,
			"0",
			worker.config.BatchSize,
			-1,
		)
		if err != nil {
			worker.logger.Error("failed to read consumer pending messages", slog.Any("error", err))
			return
		}
		if len(messages) == 0 {
			return
		}
		if !worker.handleBatch(messages) {
			return
		}
	}
}

func (worker *Worker) handleBatch(messages []redis.XMessage) bool {
	processCtx, cancel := context.WithTimeout(context.Background(), worker.config.ProcessingTimeout)
	defer cancel()

	if err := worker.retry(processCtx, func(attemptCtx context.Context) error {
		return worker.processor.Process(attemptCtx, messages)
	}); err != nil {
		worker.logger.Error("failed to process click event batch",
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
		worker.logger.Error("failed to acknowledge click event batch",
			slog.Int("count", len(messages)),
			slog.Any("error", err),
		)
		return false
	}

	worker.logger.Debug("processed click event batch", slog.Int("count", len(messages)))
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
