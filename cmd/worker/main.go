package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MohsenDowlati/shorts/internal/analytics"
	"github.com/MohsenDowlati/shorts/internal/config"
	"github.com/MohsenDowlati/shorts/internal/logging"
	"github.com/MohsenDowlati/shorts/internal/metrics"
	"github.com/MohsenDowlati/shorts/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("analytics worker failed", "error", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	env := config.NewEnv()
	logger := newLogger(env)
	slog.SetDefault(logger)

	if err := env.ValidateWorker(); err != nil {
		return fmt.Errorf("invalid worker environment: %w", err)
	}

	tracingCtx, tracingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	tracerProvider, err := telemetry.Init(tracingCtx, "shortener-analytics-worker")
	tracingCancel()
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown tracing: %w", err))
		}
	}()

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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", env.WorkerMetricsAddress)
	if err != nil {
		return fmt.Errorf("listen for worker metrics: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			cancel()
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			runErr = errors.Join(runErr, fmt.Errorf("shutdown worker metrics: %w", err))
		}
	}()
	logger.Info("worker metrics listening", "addr", env.WorkerMetricsAddress)
	workerErr := worker.Run(ctx)
	select {
	case err := <-serverErr:
		return errors.Join(workerErr, fmt.Errorf("worker metrics server: %w", err))
	default:
		return workerErr
	}
}

func newLogger(env *config.Env) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(env.LogLevel)}
	if strings.EqualFold(strings.TrimSpace(env.AppEnv), "production") {
		return slog.New(logging.NewHandler(slog.NewJSONHandler(os.Stdout, opts)))
	}
	return slog.New(logging.NewHandler(slog.NewTextHandler(os.Stdout, opts)))
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
