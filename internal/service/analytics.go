package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MohsenDowlati/shorts/internal/domain"
)

var ErrAnalyticsForbidden = errors.New("not authorized to view link analytics")

type LinkAnalyticsStore interface {
	Get(context.Context, string, string, string) (*domain.LinkAnalytics, error)
}

type LinkAnalyticsService struct {
	links     LinkStore
	analytics LinkAnalyticsStore
	timeout   time.Duration
}

func NewLinkAnalyticsService(links LinkStore, analytics LinkAnalyticsStore, timeout time.Duration) *LinkAnalyticsService {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &LinkAnalyticsService{links: links, analytics: analytics, timeout: timeout}
}

func (service *LinkAnalyticsService) Get(ctx context.Context, code, userID, from, to string) (*domain.LinkAnalytics, error) {
	ctx, cancel := context.WithTimeout(ctx, service.timeout)
	defer cancel()

	link, err := service.links.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, domain.ErrLinkNotFound) {
			return nil, ErrLinkNotFound
		}
		return nil, fmt.Errorf("get link for analytics: %w", err)
	}
	if link.OwnerID != userID {
		return nil, ErrAnalyticsForbidden
	}

	analytics, err := service.analytics.Get(ctx, code, from, to)
	if err != nil {
		return nil, fmt.Errorf("get link analytics: %w", err)
	}
	return analytics, nil
}
