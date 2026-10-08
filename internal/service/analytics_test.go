package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/domain"
)

type analyticsLinkStore struct {
	link *domain.Link
	err  error
}

func (store analyticsLinkStore) Create(context.Context, *domain.Link) error {
	return nil
}

func (store analyticsLinkStore) GetByCode(context.Context, string) (*domain.Link, error) {
	return store.link, store.err
}

type analyticsStore struct {
	result *domain.LinkAnalytics
	calls  int
}

func (store *analyticsStore) Get(context.Context, string, string, string) (*domain.LinkAnalytics, error) {
	store.calls++
	return store.result, nil
}

func TestLinkAnalyticsServiceOwnership(t *testing.T) {
	tests := []struct {
		name      string
		link      *domain.Link
		linkErr   error
		userID    string
		wantErr   error
		wantCalls int
	}{
		{
			name:      "owner",
			link:      &domain.Link{Code: "owned", OwnerID: "owner-id"},
			userID:    "owner-id",
			wantCalls: 1,
		},
		{
			name:    "different owner",
			link:    &domain.Link{Code: "private", OwnerID: "owner-id"},
			userID:  "other-user",
			wantErr: ErrAnalyticsForbidden,
		},
		{
			name:    "missing link",
			linkErr: domain.ErrLinkNotFound,
			userID:  "owner-id",
			wantErr: ErrLinkNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analytics := &analyticsStore{result: &domain.LinkAnalytics{Code: "owned"}}
			service := NewLinkAnalyticsService(
				analyticsLinkStore{link: test.link, err: test.linkErr},
				analytics,
				time.Second,
			)

			_, err := service.Get(context.Background(), "owned", test.userID, "2026-10-01", "2026-10-08")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Get() error = %v, want %v", err, test.wantErr)
			}
			if analytics.calls != test.wantCalls {
				t.Fatalf("analytics calls = %d, want %d", analytics.calls, test.wantCalls)
			}
		})
	}
}
