package handler

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

func (h *AuthHandler) Signup(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	if msg, valid := validateCredentials(req); !valid {
		WriteError(w, http.StatusBadRequest, msg)
		return
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hash password", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not process password")
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
			WriteError(w, http.StatusConflict, "username already taken")
			return
		}
		h.logger.ErrorContext(ctx, "create user", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	h.respondWithTokens(w, http.StatusCreated, user)
}

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
			WriteError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		h.logger.ErrorContext(ctx, "get user", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not sign in")
		return
	}

	if err := auth.CheckPassword(user.HashedPassword, req.Password); err != nil {
		WriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	h.respondWithTokens(w, http.StatusOK, user)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		WriteError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	claims, err := h.tokens.ParseRefresh(strings.TrimSpace(body.RefreshToken))
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	access, err := h.tokens.GenerateAccess(claims.Subject, claims.Username)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "generate access token", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"access_token": access})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, userView{
		ID:       auth.UserID(r.Context()),
		Username: auth.Username(r.Context()),
	})
}

func (h *AuthHandler) respondWithTokens(w http.ResponseWriter, status int, user *domain.User) {
	id := user.ID.Hex()
	access, err := h.tokens.GenerateAccess(id, user.Username)
	if err != nil {
		h.logger.Error("generate access token", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	refresh, err := h.tokens.GenerateRefresh(id, user.Username)
	if err != nil {
		h.logger.Error("generate refresh token", slog.Any("error", err))
		WriteError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	WriteJSON(w, status, authResponse{
		User:         userView{ID: id, Username: user.Username},
		AccessToken:  access,
		RefreshToken: refresh,
	})
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var req credentials
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body")
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
