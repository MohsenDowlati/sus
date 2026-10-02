package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/repository"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AuthHandler serves the signup/signin/refresh/me endpoints.
type AuthHandler struct {
	users   *repository.UserRepository
	tokens  *auth.TokenService
	logger  *slog.Logger
	timeout time.Duration
}

func NewAuthHandler(users *repository.UserRepository, tokens *auth.TokenService, logger *slog.Logger, timeout time.Duration) *AuthHandler {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &AuthHandler{users: users, tokens: tokens, logger: logger, timeout: timeout}
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type authResponse struct {
	User         userView `json:"user"`
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
}

// Signup creates a new account and returns a fresh token pair.
func (h *AuthHandler) Signup(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	if msg, valid := validateCredentials(req); !valid {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("hash password", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not process password")
		return
	}

	now := time.Now().UTC()
	user := &domain.User{
		ID:             primitive.NewObjectID(),
		Username:       req.Username,
		HashedPassword: hashed,
		CreatedAt:      now,
		LastVisitAt:    now,
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	if err := h.users.Create(ctx, user); err != nil {
		if errors.Is(err, domain.ErrUsernameTaken) {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		h.logger.Error("create user", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	h.respondWithTokens(w, http.StatusCreated, user)
}

// Signin verifies credentials and returns a fresh token pair.
func (h *AuthHandler) Signin(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	user, err := h.users.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			// Same response as a bad password so we don't leak which usernames exist.
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		h.logger.Error("get user", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not sign in")
		return
	}

	if err := auth.CheckPassword(user.HashedPassword, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.respondWithTokens(w, http.StatusOK, user)
}

// Refresh exchanges a valid refresh token for a new access token.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	claims, err := h.tokens.ParseRefresh(strings.TrimSpace(body.RefreshToken))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	access, err := h.tokens.GenerateAccess(claims.Subject, claims.Username)
	if err != nil {
		h.logger.Error("generate access token", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"access_token": access})
}

// Me returns the identity carried by the validated access token.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(ctxUserID).(string)
	username, _ := r.Context().Value(ctxUsername).(string)
	writeJSON(w, http.StatusOK, userView{ID: id, Username: username})
}

func (h *AuthHandler) respondWithTokens(w http.ResponseWriter, status int, user *domain.User) {
	id := user.ID.Hex()

	access, err := h.tokens.GenerateAccess(id, user.Username)
	if err != nil {
		h.logger.Error("generate access token", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}

	refresh, err := h.tokens.GenerateRefresh(id, user.Username)
	if err != nil {
		h.logger.Error("generate refresh token", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}

	writeJSON(w, status, authResponse{
		User:         userView{ID: id, Username: user.Username},
		AccessToken:  access,
		RefreshToken: refresh,
	})
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var req credentials
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	req.Username = strings.TrimSpace(req.Username)
	return req, true
}

func validateCredentials(req credentials) (string, bool) {
	if len(req.Username) < 3 || len(req.Username) > 32 {
		return "username must be between 3 and 32 characters", false
	}
	if len(req.Password) < 8 {
		return "password must be at least 8 characters", false
	}
	return "", true
}
