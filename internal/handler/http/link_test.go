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
)

type duplicateLinkStore struct{}

func (duplicateLinkStore) Create(context.Context, *domain.Link) error {
	return domain.ErrSlugReserved
}

func (duplicateLinkStore) GetByCode(context.Context, string) (*domain.Link, error) {
	return nil, domain.ErrLinkNotFound
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
