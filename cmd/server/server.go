package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MohsenDowlati/shorts/internal/analytics"
	"github.com/MohsenDowlati/shorts/internal/api"
	"github.com/MohsenDowlati/shorts/internal/auth"
	cache "github.com/MohsenDowlati/shorts/internal/cache"
	"github.com/MohsenDowlati/shorts/internal/config"
	httphandler "github.com/MohsenDowlati/shorts/internal/handler/http"
	"github.com/MohsenDowlati/shorts/internal/ratelimit"
	"github.com/MohsenDowlati/shorts/internal/repository"
	"github.com/MohsenDowlati/shorts/internal/service"
	"github.com/MohsenDowlati/shorts/internal/urlguard"
)

// Run bootstraps configuration, logging, storage and the HTTP server, then
// blocks until an interrupt triggers a graceful shutdown.
func Run() (runErr error) {
	env := config.NewEnv()

	logger := newLogger(env)
	slog.SetDefault(logger)

	if err := env.Validate(); err != nil {
		return fmt.Errorf("invalid environment: %w", err)
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), env.RedisDialTimeout)
	app, err := AppWithEnv(startupCtx, env)
	startupCancel()
	if err != nil {
		return fmt.Errorf("initialize application dependencies: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Close(closeCtx); err != nil {
			if runErr == nil {
				runErr = err
				return
			}
			logger.Error("dependency shutdown failed", slog.Any("error", err))
		}
	}()

	db := app.Mongo.Database(env.DBName)

	userRepo := repository.NewUserRepository(db)
	linkRepo := repository.NewLinkRepository(db)
	tokens := auth.NewTokenService(
		env.AccessTokenSecret,
		env.RefreshTokenSecret,
		time.Duration(env.AccessTokenExpiryHour)*time.Hour,
		time.Duration(env.RefreshTokenExpiryHour)*time.Hour,
	)

	requestTimeout := time.Duration(env.ContextTimeout) * time.Second
	authHandler := httphandler.NewAuthHandler(userRepo, tokens, logger, requestTimeout)
	clientIPs, err := ratelimit.NewClientIPResolver(env.TrustedProxyCIDRs)
	if err != nil {
		return fmt.Errorf("initialize client IP resolver: %w", err)
	}
	linkService := service.NewLinkServiceWithValidator(
		linkRepo,
		cache.NewRedis(app.Redis),
		urlguard.NewDefault(env.ShortenerDomains),
		logger,
		requestTimeout,
	)
	analyticsService := service.NewLinkAnalyticsService(
		linkRepo,
		repository.NewLinkAnalyticsRepository(db),
		requestTimeout,
	)
	clickTracker := analytics.NewTracker(
		analytics.NewRedisStreamPublisherForStream(app.Redis, env.RedisAnalyticsStream),
		clientIPs,
		env.AnalyticsIPSalt,
		logger,
		1024,
	)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := clickTracker.Close(closeCtx); err != nil {
			if runErr == nil {
				runErr = fmt.Errorf("close click analytics: %w", err)
				return
			}
			logger.Error("click analytics shutdown failed", slog.Any("error", err))
		}
	}()
	linkHandler := httphandler.NewLinkHandlerWithAnalytics(linkService, clickTracker, logger)
	analyticsHandler := httphandler.NewAnalyticsHandlerWithMaxQueryDays(analyticsService, logger, env.AnalyticsMaxQueryDays)
	rateLimiter := ratelimit.NewWithClientIPResolver(ratelimit.NewRedisStore(app.Redis), logger, clientIPs)
	router := api.NewRouterWithAnalytics(authHandler, linkHandler, analyticsHandler, tokens, rateLimiter, logger)

	srv := &http.Server{
		Addr:              env.ServerAddress,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", slog.String("addr", env.ServerAddress), slog.String("env", env.AppEnv))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("server stopped")
	return nil
}

// newLogger builds an slog logger: human-friendly text in development, JSON in
// production, at the level named by LOG_LEVEL.
func newLogger(env *config.Env) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(env.LogLevel)}

	var handler slog.Handler
	if strings.EqualFold(env.AppEnv, "production") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
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
