package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MohsenDowlati/shorts/internal/analytics"
	"github.com/MohsenDowlati/shorts/internal/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("analytics worker failed: %v", err)
	}
}

func run() (runErr error) {
	env := config.NewEnv()
	logger := newLogger(env)
	slog.SetDefault(logger)

	if err := env.ValidateWorker(); err != nil {
		return fmt.Errorf("invalid worker environment: %w", err)
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), env.RedisDialTimeout)
	mongoClient := config.NewMongoDatabase(env)
	redisClient, err := config.NewRedisClient(startupCtx, env)
	startupCancel()
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(err, config.CloseMongoDBConnection(closeCtx, mongoClient))
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		closeErr := errors.Join(
			config.CloseRedisClient(redisClient),
			config.CloseMongoDBConnection(closeCtx, mongoClient),
		)
		if closeErr != nil {
			if runErr == nil {
				runErr = fmt.Errorf("close worker dependencies: %w", closeErr)
				return
			}
			logger.Error("worker dependency shutdown failed", slog.Any("error", closeErr))
		}
	}()

	worker := analytics.NewWorker(
		analytics.NewRedisStreamConsumer(redisClient),
		analytics.NewMongoBatchProcessor(mongoClient.Database(env.DBName)),
		analytics.WorkerConfig{
			Stream:            env.RedisAnalyticsStream,
			ConsumerGroup:     env.RedisAnalyticsConsumerGroup,
			ConsumerName:      env.RedisAnalyticsConsumerName,
			BatchSize:         int64(env.AnalyticsBatchSize),
			PollTimeout:       env.AnalyticsPollTimeout,
			RetryMaxAttempts:  env.AnalyticsMaxRetries + 1,
			RetryBackoff:      env.AnalyticsRetryBackoff,
			PendingMinIdle:    env.AnalyticsPendingMinIdle,
			ProcessingTimeout: env.AnalyticsProcessTimeout,
		},
		logger,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return worker.Run(ctx)
}

func newLogger(env *config.Env) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(env.LogLevel)}
	if strings.EqualFold(env.AppEnv, "production") {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
