package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	httphandler "github.com/MohsenDowlati/shorts/internal/handler/http"
	"github.com/MohsenDowlati/shorts/internal/ratelimit"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// NewRouter wires the server without the optional analytics endpoint.
func NewRouter(authHandler *httphandler.AuthHandler, linkHandler *httphandler.LinkHandler, tokens *auth.TokenService, limiter *ratelimit.Limiter, logger *slog.Logger) *chi.Mux {
	return NewRouterWithAnalytics(authHandler, linkHandler, nil, tokens, limiter, logger)
}

// NewRouterWithAnalytics adds the authenticated, rate-limited link analytics endpoint.
func NewRouterWithAnalytics(authHandler *httphandler.AuthHandler, linkHandler *httphandler.LinkHandler, analyticsHandler *httphandler.AnalyticsHandler, tokens *auth.TokenService, limiter *ratelimit.Limiter, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(auth.RequestLogger(logger))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httphandler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/signup", authHandler.Signup)
		r.Post("/signin", authHandler.Signin)
		r.Post("/refresh", authHandler.Refresh)
		r.With(auth.Authenticator(tokens)).Get("/me", authHandler.Me)
	})

	createLinkLimit := limiter.Middleware(ratelimit.Policy{
		Name:    "create-link",
		Limit:   20,
		Window:  time.Minute,
		KeyFunc: limiter.UserOrIP,
	})
	slugCheckLimit := limiter.Middleware(ratelimit.Policy{
		Name:    "check-slug",
		Limit:   60,
		Window:  time.Minute,
		KeyFunc: limiter.IP,
	})
	analyticsLimit := limiter.Middleware(ratelimit.Policy{
		Name:    "link-analytics",
		Limit:   60,
		Window:  time.Minute,
		KeyFunc: limiter.UserOrIP,
	})

	r.With(auth.Authenticator(tokens), createLinkLimit).Post("/api/v1/links", linkHandler.ShorteningLink)
	if analyticsHandler != nil {
		r.With(auth.Authenticator(tokens), analyticsLimit).Get("/api/v1/links/{code}/analytics", analyticsHandler.LinkAnalytics)
	}
	r.With(slugCheckLimit).Get("/api/v1/links/check-slug", linkHandler.CheckSlug)
	r.With(slugCheckLimit).Post("/api/v1/links/check-slug", linkHandler.CheckSlug)
	r.Get("/{code}", linkHandler.Redirect)

	return r
}
