package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/service"
	"github.com/go-chi/chi/v5"
)

type duplicateLinkStore struct{}

func (duplicateLinkStore) Create(context.Context, *domain.Link) error {
	return domain.ErrSlugReserved
}

type handlerCache struct {
	value string
}

func (cache handlerCache) Get(context.Context, string) (string, bool, error) {
	return cache.value, true, nil
}

func (handlerCache) Set(context.Context, string, string, time.Duration) error {
	return nil
}

func (duplicateLinkStore) GetByCode(context.Context, string) (*domain.Link, error) {
	return nil, domain.ErrLinkNotFound
}

func TestLinkHandlerRedirect_CachedResponses(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	tests := []struct {
		name         string
		cachedValue  string
		wantStatus   int
		wantLocation string
	}{
		{name: "null marker", cachedValue: "", wantStatus: http.StatusNotFound},
		{name: "disabled", cachedValue: `{"original_url":"https://example.com","is_disabled":true}`, wantStatus: http.StatusGone},
		{name: "expired", cachedValue: `{"original_url":"https://example.com","expires_at":"` + expired + `"}`, wantStatus: http.StatusGone},
		{name: "valid", cachedValue: `{"original_url":"https://example.com/destination"}`, wantStatus: http.StatusTemporaryRedirect, wantLocation: "https://example.com/destination"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			linkService := service.NewLinkServiceWithCache(duplicateLinkStore{}, handlerCache{value: test.cachedValue}, logger, time.Second)
			linkHandler := NewLinkHandler(linkService, logger)
			router := chi.NewRouter()
			router.Get("/{code}", linkHandler.Redirect)
			request := httptest.NewRequest(http.MethodGet, "/cached-code", nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if location := response.Header().Get("Location"); location != test.wantLocation {
				t.Fatalf("Location = %q, want %q", location, test.wantLocation)
			}
		})
	}
}

func TestLinkHandlerCreate_CustomDuplicateReturnsConflict(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	linkService := service.NewLinkService(duplicateLinkStore{}, logger, time.Second)
	handler := NewLinkHandler(linkService, logger)
	tokens := auth.NewTokenService(
		"access-secret-used-only-for-this-unit-test",
		"refresh-secret-used-only-for-this-unit-test",
		time.Hour,
		time.Hour,
	)
	accessToken, err := tokens.GenerateAccess("owner-id", "test-user")
	if err != nil {
		t.Fatalf("GenerateAccess() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", strings.NewReader(`{
		"original_url":"https://example.com/article",
		"type":"custom",
		"code":"existing-code"
	}`))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()

	auth.Authenticator(tokens)(http.HandlerFunc(handler.ShorteningLink)).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}
