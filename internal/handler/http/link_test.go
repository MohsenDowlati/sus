package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/service"
	"github.com/MohsenDowlati/shorts/internal/urlguard"
	"github.com/go-chi/chi/v5"
)

type duplicateLinkStore struct{}

func (duplicateLinkStore) Create(context.Context, *domain.Link) error {
	return domain.ErrSlugReserved
}

type recordingLinkStore struct {
	creates int
}

func (store *recordingLinkStore) Create(context.Context, *domain.Link) error {
	store.creates++
	return nil
}

func (*recordingLinkStore) GetByCode(context.Context, string) (*domain.Link, error) {
	return nil, domain.ErrLinkNotFound
}

type handlerResolver struct {
	addresses map[string][]netip.Addr
}

type recordingClickTracker struct {
	codes []string
}

func (tracker *recordingClickTracker) Track(_ *http.Request, code string) {
	tracker.codes = append(tracker.codes, code)
}

func (resolver handlerResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return resolver.addresses[host], nil
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

func TestLinkHandlerRedirect_TracksOnlyValidActiveLinks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracker := &recordingClickTracker{}
	linkService := service.NewLinkServiceWithCache(
		duplicateLinkStore{},
		handlerCache{value: `{"original_url":"https://example.com/destination"}`},
		logger,
		time.Second,
	)
	linkHandler := NewLinkHandlerWithAnalytics(linkService, tracker, logger)
	router := chi.NewRouter()
	router.Get("/{code}", linkHandler.Redirect)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tracked-code", nil))

	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTemporaryRedirect)
	}
	if len(tracker.codes) != 1 || tracker.codes[0] != "tracked-code" {
		t.Fatalf("tracked codes = %v, want [tracked-code]", tracker.codes)
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
		"original_url":"https://8.8.8.8/article",
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

func TestLinkHandlerCheckSlug_GETAndPOST(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	linkService := service.NewLinkService(duplicateLinkStore{}, logger, time.Second)
	linkHandler := NewLinkHandler(linkService, logger)

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "GET", method: http.MethodGet, target: "/api/v1/links/check-slug?slug=available-slug"},
		{name: "POST", method: http.MethodPost, target: "/api/v1/links/check-slug", body: `{"slug":"available-slug"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			response := httptest.NewRecorder()
			linkHandler.CheckSlug(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"available":true`) {
				t.Fatalf("body = %s, want available=true", response.Body.String())
			}
		})
	}
}

func TestLinkHandlerCreate_RejectsUnsafeAndCircularTargets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &recordingLinkStore{}
	validator := urlguard.New(handlerResolver{addresses: map[string][]netip.Addr{
		"sho.rt": {netip.MustParseAddr("8.8.8.8")},
	}}, []string{"sho.rt"})
	linkService := service.NewLinkServiceWithValidator(store, nil, validator, logger, time.Second)
	linkHandler := NewLinkHandler(linkService, logger)
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

	targets := []string{
		"http://localhost:8080",
		"http://127.0.0.1",
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.1",
		"https://sho.rt/existing-code",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			body := `{"original_url":"` + target + `","type":"auto"}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/links", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+accessToken)
			response := httptest.NewRecorder()

			auth.Authenticator(tokens)(http.HandlerFunc(linkHandler.ShorteningLink)).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
	if store.creates != 0 {
		t.Fatalf("repository writes = %d, want 0", store.creates)
	}
}
