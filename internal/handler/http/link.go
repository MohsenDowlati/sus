package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/service"
	"github.com/go-chi/chi/v5"
)

type createLinkRequest struct {
	OriginalURL string          `json:"original_url"`
	Type        domain.LinkType `json:"type"`
	Code        *string         `json:"code,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
}

type createLinkResponse struct {
	*domain.Link
	ShortURL string `json:"short_url"`
}

type LinkHandler struct {
	service *service.LinkService
	logger  *slog.Logger
}

func NewLinkHandler(svc *service.LinkService, logger *slog.Logger) *LinkHandler {
	return &LinkHandler{
		service: svc,
		logger:  logger.With(slog.String("component", "link_handler")),
	}
}

func (h *LinkHandler) ShorteningLink(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.WarnContext(r.Context(), "invalid json body in request", slog.Any("error", err))
		WriteError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	userID := auth.UserID(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	params := service.CreateLinkParams{
		OriginalURL: req.OriginalURL,
		Type:        req.Type,
		Code:        req.Code,
		ExpiresAt:   req.ExpiresAt,
		OwnerID:     userID,
	}

	link, err := h.service.CreateLink(r.Context(), params)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidSlug),
			errors.Is(err, service.ErrSlugRequired),
			errors.Is(err, service.ErrExpirationInPast),
			errors.Is(err, service.ErrInvalidURL):
			WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, domain.ErrSlugReserved):
			WriteError(w, http.StatusConflict, "slug is already in use")
		default:
			WriteError(w, http.StatusInternalServerError, "failed to create link")
		}
		return
	}

	WriteJSON(w, http.StatusCreated, createLinkResponse{
		Link:     link,
		ShortURL: requestBaseURL(r) + "/" + link.Code,
	})
}

func (h *LinkHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	link, err := h.service.Resolve(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrLinkNotFound):
			WriteError(w, http.StatusNotFound, "link not found")
		case errors.Is(err, service.ErrLinkUnavailable):
			WriteError(w, http.StatusGone, "link is no longer available")
		default:
			h.logger.ErrorContext(r.Context(), "failed to resolve link", slog.Any("error", err))
			WriteError(w, http.StatusInternalServerError, "failed to resolve link")
		}
		return
	}
	http.Redirect(w, r, link.OriginalURL, http.StatusTemporaryRedirect)
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedScheme := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwardedScheme == "http" || forwardedScheme == "https" {
		scheme = forwardedScheme
	}
	return scheme + "://" + r.Host
}
