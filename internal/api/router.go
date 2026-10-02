package api

import (
	"log/slog"
	"net/http"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// NewRouter wires the chi router: shared middleware, a health check, and the
// auth routes (signup/signin/refresh are public; /me is token-protected).
func NewRouter(authHandler *AuthHandler, tokens *auth.TokenService, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(RequestLogger(logger))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/signup", authHandler.Signup)
		r.Post("/signin", authHandler.Signin)
		r.Post("/refresh", authHandler.Refresh)
		r.With(Authenticator(tokens)).Get("/me", authHandler.Me)
	})

	return r
}
