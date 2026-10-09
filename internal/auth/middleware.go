package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MohsenDowlati/shorts/internal/logging"
)

type ctxKey string

const (
	ctxUserID   ctxKey = "user_id"
	ctxUsername ctxKey = "username"
)

// RequestLogger records sanitized access events after each request completes.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return logging.RequestLogger(logger)
}

// Authenticator validates the Bearer access token and injects the user id and
// username into the request context for downstream handlers.
func Authenticator(tokens *TokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			scheme, token, found := strings.Cut(header, " ")
			if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
				writeAuthError(w, "missing or malformed bearer token")
				return
			}

			claims, err := tokens.ParseAccess(strings.TrimSpace(token))
			if err != nil {
				writeAuthError(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), ctxUserID, claims.Subject)
			ctx = context.WithValue(ctx, ctxUsername, claims.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserID(ctx context.Context) string {
	userID, _ := ctx.Value(ctxUserID).(string)
	return userID
}

func Username(ctx context.Context) string {
	username, _ := ctx.Value(ctxUsername).(string)
	return username
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
