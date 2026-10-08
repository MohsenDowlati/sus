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

type stubAnalyticsProvider struct {
	result *domain.LinkAnalytics
	err    error
	code   string
	userID string
	from   string
	to     string
}

func (provider *stubAnalyticsProvider) Get(_ context.Context, code, userID, from, to string) (*domain.LinkAnalytics, error) {
	provider.code = code
	provider.userID = userID
	provider.from = from
	provider.to = to
	return provider.result, provider.err
}

func TestAnalyticsHandlerReturnsFilteredAnalytics(t *testing.T) {
	provider := &stubAnalyticsProvider{result: &domain.LinkAnalytics{
		Code:        "my-link",
		From:        "2026-10-01",
		To:          "2026-10-08",
		TotalClicks: 12,
		Daily:       []domain.DailyClickTotal{{Date: "2026-10-08", TotalClicks: 12}},
		Referrers:   map[string]int64{"twitter": 8, "direct": 4},
		Browsers:    map[string]int64{"chrome": 12},
	}}
	handler := newTestAnalyticsHandler(provider)
	response := serveAuthenticatedAnalytics(t, handler, "/api/v1/links/my-link/analytics?from=2026-10-01&to=2026-10-08")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if provider.code != "my-link" || provider.userID != "owner-id" {
		t.Fatalf("provider identity = %q/%q", provider.code, provider.userID)
	}
	if provider.from != "2026-10-01" || provider.to != "2026-10-08" {
		t.Fatalf("provider range = %s..%s", provider.from, provider.to)
	}
	for _, expected := range []string{`"total_clicks":12`, `"twitter":8`, `"chrome":12`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body = %s, want %s", response.Body.String(), expected)
		}
	}
}

func TestAnalyticsHandlerMapsAuthorizationErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "missing", err: service.ErrLinkNotFound, wantStatus: http.StatusNotFound},
		{name: "forbidden", err: service.ErrAnalyticsForbidden, wantStatus: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newTestAnalyticsHandler(&stubAnalyticsProvider{err: test.err})
			response := serveAuthenticatedAnalytics(t, handler, "/api/v1/links/my-link/analytics")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestParseAnalyticsDateRange(t *testing.T) {
	now := time.Date(2026, time.October, 8, 18, 0, 0, 0, time.FixedZone("UTC+3:30", 3*60*60+30*60))
	tests := []struct {
		name     string
		target   string
		wantFrom string
		wantTo   string
		wantErr  string
	}{
		{name: "default", target: "/analytics", wantFrom: "2026-09-09", wantTo: "2026-10-08"},
		{name: "explicit", target: "/analytics?from=2026-10-01&to=2026-10-08", wantFrom: "2026-10-01", wantTo: "2026-10-08"},
		{name: "invalid format", target: "/analytics?from=10-01-2026", wantErr: "from must use YYYY-MM-DD format"},
		{name: "reversed", target: "/analytics?from=2026-10-08&to=2026-10-01", wantErr: "from must be on or before to"},
		{name: "too large", target: "/analytics?from=2025-10-01&to=2026-10-08", wantErr: "date range must not exceed 366 days"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			from, to, err := parseAnalyticsDateRange(request, now)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAnalyticsDateRange() error = %v", err)
			}
			if from != test.wantFrom || to != test.wantTo {
				t.Fatalf("range = %s..%s, want %s..%s", from, to, test.wantFrom, test.wantTo)
			}
		})
	}
}

func TestParseAnalyticsDateRangeUsesConfiguredMaximum(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/analytics?from=2026-10-01&to=2026-10-11", nil)
	_, _, err := parseAnalyticsDateRangeWithMaxDays(request, time.Date(2026, time.October, 8, 0, 0, 0, 0, time.UTC), 10)
	if err == nil || err.Error() != "date range must not exceed 10 days" {
		t.Fatalf("error = %v, want configured maximum error", err)
	}
}

func newTestAnalyticsHandler(provider LinkAnalyticsProvider) *AnalyticsHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewAnalyticsHandler(provider, logger)
	handler.now = func() time.Time { return time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC) }
	return handler
}

func serveAuthenticatedAnalytics(t *testing.T, handler *AnalyticsHandler, target string) *httptest.ResponseRecorder {
	t.Helper()
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
	router := chi.NewRouter()
	router.With(auth.Authenticator(tokens)).Get("/api/v1/links/{code}/analytics", handler.LinkAnalytics)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
