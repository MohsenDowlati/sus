package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/MohsenDowlati/shorts/internal/domain"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/sync/singleflight"
)

var (
	ErrInvalidSlug      = errors.New("invalid or reserved custom slug")
	ErrSlugRequired     = errors.New("slug is required for custom link type")
	ErrExpirationInPast = errors.New("expiration time must be in the future")
	ErrInvalidURL       = errors.New("original_url must be a valid http or https URL")
	ErrLinkNotFound     = errors.New("link not found")
	ErrLinkUnavailable  = errors.New("link is disabled or expired")
)

const (
	maxAutoSlugAttempts = 3
	negativeCacheTTL    = 60 * time.Second
	defaultLinkCacheTTL = 15 * time.Minute
)

type LinkStore interface {
	Create(context.Context, *domain.Link) error
	GetByCode(context.Context, string) (*domain.Link, error)
}

type LinkCache interface {
	Get(context.Context, string) (value string, found bool, err error)
	Set(context.Context, string, string, time.Duration) error
}

type CreateLinkParams struct {
	OriginalURL string
	Type        domain.LinkType
	Code        *string
	ExpiresAt   *time.Time
	OwnerID     string
}

type LinkService struct {
	links        LinkStore
	logger       *slog.Logger
	timeout      time.Duration
	generateSlug func() (string, error)
	cache        LinkCache
	cacheTTL     time.Duration
	lookupGroup  singleflight.Group
}

func NewLinkService(links LinkStore, logger *slog.Logger, timeout time.Duration) *LinkService {
	return NewLinkServiceWithCache(links, nil, logger, timeout)
}

func NewLinkServiceWithCache(links LinkStore, cache LinkCache, logger *slog.Logger, timeout time.Duration) *LinkService {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &LinkService{
		links:        links,
		logger:       logger.With(slog.String("component", "link_service")),
		timeout:      timeout,
		generateSlug: domain.AutoGenerateSlug,
		cache:        cache,
		cacheTTL:     defaultLinkCacheTTL,
	}
}

func (s *LinkService) Create(ctx context.Context, params CreateLinkParams) (*domain.Link, error) {
	return s.CreateLink(ctx, params)
}

func (s *LinkService) CreateLink(ctx context.Context, params CreateLinkParams) (*domain.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	originalURL, err := normalizeOriginalURL(params.OriginalURL)
	if err != nil {
		return nil, ErrInvalidURL
	}

	switch params.Type {
	case domain.LinkTypeCustom:
		if params.Code == nil || strings.TrimSpace(*params.Code) == "" {
			return nil, ErrSlugRequired
		}

		customCode := strings.TrimSpace(*params.Code)
		isValid, err := domain.ValidateSlug(customCode)
		if err != nil || !isValid {
			s.logger.WarnContext(ctx, "custom slug validation failed",
				slog.String("slug", customCode),
				slog.Any("error", err),
			)
			return nil, ErrInvalidSlug
		}
		return s.persistLink(ctx, params, originalURL, customCode)

	case domain.LinkTypeAuto:
		for attempt := 0; attempt < maxAutoSlugAttempts; attempt++ {
			generated, err := s.generateSlug()
			if err != nil {
				s.logger.ErrorContext(ctx, "failed to generate slug", slog.Any("error", err))
				return nil, fmt.Errorf("generate slug: %w", err)
			}

			link, err := s.persistLink(ctx, params, originalURL, generated)
			if err == nil {
				return link, nil
			}
			if !errors.Is(err, domain.ErrSlugReserved) {
				return nil, err
			}
			s.logger.WarnContext(ctx, "generated slug already exists",
				slog.String("code", generated),
				slog.Int("attempt", attempt+1),
			)
		}
		return nil, fmt.Errorf("persist auto-generated slug: %w", domain.ErrSlugGenerationFailed)

	default:
		return nil, errors.New("unsupported link type")
	}
}

func (s *LinkService) persistLink(ctx context.Context, params CreateLinkParams, originalURL, code string) (*domain.Link, error) {
	if params.ExpiresAt != nil && params.ExpiresAt.Before(time.Now().UTC()) {
		return nil, ErrExpirationInPast
	}

	now := time.Now().UTC()
	link := &domain.Link{
		ID:          primitive.NewObjectID(),
		Code:        code,
		OriginalURL: originalURL,
		Type:        params.Type,
		OwnerID:     params.OwnerID,
		CreatedAt:   now,
		ExpiresAt:   params.ExpiresAt,
		IsDisabled:  false,
	}

	if err := s.links.Create(ctx, link); err != nil {
		if !errors.Is(err, domain.ErrSlugReserved) {
			s.logger.ErrorContext(ctx, "failed to persist link to repository",
				slog.String("code", code),
				slog.String("owner_id", params.OwnerID),
				slog.Any("error", err),
			)
		}
		return nil, fmt.Errorf("persist link: %w", err)
	}

	s.logger.InfoContext(ctx, "short link created successfully",
		slog.String("code", code),
		slog.String("owner_id", params.OwnerID),
		slog.String("type", string(params.Type)),
	)

	return link, nil
}

func (s *LinkService) Resolve(ctx context.Context, code string) (*domain.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	code = strings.TrimSpace(code)
	if link, resolved, err := s.resolveFromCache(ctx, code); resolved {
		return link, err
	}

	result, err, _ := s.lookupGroup.Do(code, func() (any, error) {
		if link, resolved, cacheErr := s.resolveFromCache(ctx, code); resolved {
			return link, cacheErr
		}
		return s.resolveFromStore(ctx, code)
	})
	if err != nil {
		return nil, err
	}
	link, ok := result.(*domain.Link)
	if !ok || link == nil {
		return nil, errors.New("invalid link lookup result")
	}
	return link, nil
}

type cachedLink struct {
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsDisabled  bool       `json:"is_disabled"`
}

func (s *LinkService) resolveFromCache(ctx context.Context, code string) (*domain.Link, bool, error) {
	if s.cache == nil {
		return nil, false, nil
	}

	value, found, err := s.cache.Get(ctx, linkCacheKey(code))
	if err != nil {
		s.logger.WarnContext(ctx, "link cache lookup failed", slog.String("code", code), slog.Any("error", err))
		return nil, false, nil
	}
	if !found {
		return nil, false, nil
	}
	if value == "" {
		return nil, true, ErrLinkNotFound
	}

	var cached cachedLink
	if err := json.Unmarshal([]byte(value), &cached); err != nil {
		s.logger.WarnContext(ctx, "invalid cached link payload", slog.String("code", code), slog.Any("error", err))
		return nil, false, nil
	}
	if cached.IsDisabled || cached.ExpiresAt != nil && !cached.ExpiresAt.After(time.Now().UTC()) {
		return nil, true, ErrLinkUnavailable
	}
	return &domain.Link{
		Code:        code,
		OriginalURL: cached.OriginalURL,
		ExpiresAt:   cached.ExpiresAt,
		IsDisabled:  cached.IsDisabled,
	}, true, nil
}

func (s *LinkService) resolveFromStore(ctx context.Context, code string) (*domain.Link, error) {
	link, err := s.links.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, domain.ErrLinkNotFound) {
			s.cacheValue(ctx, code, "", negativeCacheTTL)
			return nil, ErrLinkNotFound
		}
		return nil, fmt.Errorf("resolve link: %w", err)
	}
	if link.IsDisabled || link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
		return nil, ErrLinkUnavailable
	}

	payload, err := json.Marshal(cachedLink{
		OriginalURL: link.OriginalURL,
		ExpiresAt:   link.ExpiresAt,
		IsDisabled:  link.IsDisabled,
	})
	if err != nil {
		s.logger.WarnContext(ctx, "failed to encode link cache payload", slog.String("code", code), slog.Any("error", err))
		return link, nil
	}
	s.cacheValue(ctx, code, string(payload), s.linkCacheTTL(link, time.Now().UTC()))
	return link, nil
}

func (s *LinkService) cacheValue(ctx context.Context, code, value string, ttl time.Duration) {
	if s.cache == nil || ttl <= 0 {
		return
	}
	if err := s.cache.Set(ctx, linkCacheKey(code), value, ttl); err != nil {
		s.logger.WarnContext(ctx, "failed to cache link", slog.String("code", code), slog.Any("error", err))
	}
}

func (s *LinkService) linkCacheTTL(link *domain.Link, now time.Time) time.Duration {
	ttl := s.cacheTTL
	if link.ExpiresAt != nil {
		remaining := link.ExpiresAt.Sub(now)
		if remaining < ttl {
			ttl = remaining
		}
	}
	return ttl
}

func linkCacheKey(code string) string {
	return "link:" + code
}

func normalizeOriginalURL(rawURL string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrInvalidURL
	}
	return parsed.String(), nil
}
