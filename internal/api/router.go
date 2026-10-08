package api

import (
	"log/slog"
	"net/http"

	"github.com/MohsenDowlati/shorts/internal/auth"
	httphandler "github.com/MohsenDowlati/shorts/internal/handler/http"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// NewRouter wires shared middleware, health and auth endpoints, protected link
// creation, and public short-link redirects.
func NewRouter(authHandler *httphandler.AuthHandler, linkHandler *httphandler.LinkHandler, tokens *auth.TokenService, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
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

	r.With(auth.Authenticator(tokens)).Post("/api/v1/links", linkHandler.ShorteningLink)
	r.Get("/{code}", linkHandler.Redirect)

	return r
}
