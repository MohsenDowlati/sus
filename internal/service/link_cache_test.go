package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/domain"
)

type cacheTestStore struct {
	get func(string) (*domain.Link, error)
}

func (store *cacheTestStore) Create(context.Context, *domain.Link) error {
	return nil
}

func (store *cacheTestStore) GetByCode(_ context.Context, code string) (*domain.Link, error) {
	return store.get(code)
}

type cacheWrite struct {
	key   string
	value string
	ttl   time.Duration
}

type fakeLinkCache struct {
	mu      sync.Mutex
	values  map[string]string
	writes  []cacheWrite
	getHook func()
}

func (cache *fakeLinkCache) Get(_ context.Context, key string) (string, bool, error) {
	if cache.getHook != nil {
		cache.getHook()
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	value, found := cache.values[key]
	return value, found, nil
}

func (cache *fakeLinkCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.values == nil {
		cache.values = make(map[string]string)
	}
	cache.values[key] = value
	cache.writes = append(cache.writes, cacheWrite{key: key, value: value, ttl: ttl})
	return nil
}

func TestLinkServiceResolve_NullMarkerRejectsWithoutDatabaseLookup(t *testing.T) {
	databaseCalls := 0
	store := &cacheTestStore{get: func(string) (*domain.Link, error) {
		databaseCalls++
		return nil, domain.ErrLinkNotFound
	}}
	cache := &fakeLinkCache{values: map[string]string{"link:missing": ""}}
	service := newCachedTestLinkService(store, cache)

	_, err := service.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrLinkNotFound)
	}
	if databaseCalls != 0 {
		t.Fatalf("database calls = %d, want 0", databaseCalls)
	}
}

func TestLinkServiceResolve_ValidCacheHit(t *testing.T) {
	payload, err := json.Marshal(cachedLink{OriginalURL: "https://example.com/cached"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	databaseCalls := 0
	store := &cacheTestStore{get: func(string) (*domain.Link, error) {
		databaseCalls++
		return nil, domain.ErrLinkNotFound
	}}
	cache := &fakeLinkCache{values: map[string]string{"link:cached": string(payload)}}
	service := newCachedTestLinkService(store, cache)

	link, err := service.Resolve(context.Background(), "cached")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if link.OriginalURL != "https://example.com/cached" {
		t.Fatalf("OriginalURL = %q, want cached URL", link.OriginalURL)
	}
	if databaseCalls != 0 {
		t.Fatalf("database calls = %d, want 0", databaseCalls)
	}
}

func TestLinkServiceResolve_UnavailableCacheHit(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Minute)
	tests := []cachedLink{
		{OriginalURL: "https://example.com", IsDisabled: true},
		{OriginalURL: "https://example.com", ExpiresAt: &expired},
	}

	for _, cached := range tests {
		payload, err := json.Marshal(cached)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		service := newCachedTestLinkService(
			&cacheTestStore{get: func(string) (*domain.Link, error) {
				t.Fatal("database must not be queried for unavailable cache hit")
				return nil, nil
			}},
			&fakeLinkCache{values: map[string]string{"link:gone": string(payload)}},
		)

		_, err = service.Resolve(context.Background(), "gone")
		if !errors.Is(err, ErrLinkUnavailable) {
			t.Fatalf("Resolve() error = %v, want %v", err, ErrLinkUnavailable)
		}
	}
}

func TestLinkServiceResolve_CachesNotFoundMarker(t *testing.T) {
	databaseCalls := 0
	store := &cacheTestStore{get: func(string) (*domain.Link, error) {
		databaseCalls++
		return nil, domain.ErrLinkNotFound
	}}
	cache := &fakeLinkCache{values: make(map[string]string)}
	service := newCachedTestLinkService(store, cache)

	_, err := service.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrLinkNotFound)
	}
	if len(cache.writes) != 1 {
		t.Fatalf("cache writes = %d, want 1", len(cache.writes))
	}
	write := cache.writes[0]
	if write.key != "link:missing" || write.value != "" || write.ttl != negativeCacheTTL {
		t.Fatalf("cache write = %+v, want null marker with %s TTL", write, negativeCacheTTL)
	}

	_, err = service.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("second Resolve() error = %v, want %v", err, ErrLinkNotFound)
	}
	if databaseCalls != 1 {
		t.Fatalf("database calls after repeated miss = %d, want 1", databaseCalls)
	}
}

func TestLinkServiceResolve_UsesExpirationBoundedTTL(t *testing.T) {
	expiresAt := time.Now().UTC().Add(30 * time.Second)
	store := &cacheTestStore{get: func(code string) (*domain.Link, error) {
		return &domain.Link{Code: code, OriginalURL: "https://example.com", ExpiresAt: &expiresAt}, nil
	}}
	cache := &fakeLinkCache{values: make(map[string]string)}
	service := newCachedTestLinkService(store, cache)

	if _, err := service.Resolve(context.Background(), "short-lived"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(cache.writes) != 1 {
		t.Fatalf("cache writes = %d, want 1", len(cache.writes))
	}
	ttl := cache.writes[0].ttl
	if ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("cache TTL = %s, want between 0 and 30s", ttl)
	}
}

func TestLinkServiceLinkCacheTTL(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	service := &LinkService{cacheTTL: 15 * time.Minute}

	shortExpiration := now.Add(90 * time.Second)
	if ttl := service.linkCacheTTL(&domain.Link{ExpiresAt: &shortExpiration}, now); ttl != 90*time.Second {
		t.Fatalf("short-lived TTL = %s, want 90s", ttl)
	}

	longExpiration := now.Add(time.Hour)
	if ttl := service.linkCacheTTL(&domain.Link{ExpiresAt: &longExpiration}, now); ttl != 15*time.Minute {
		t.Fatalf("long-lived TTL = %s, want 15m", ttl)
	}
}

func TestLinkServiceResolve_CoalescesConcurrentCacheMisses(t *testing.T) {
	const callers = 20

	var databaseCalls atomic.Int32
	releaseDatabase := make(chan struct{})
	store := &cacheTestStore{get: func(code string) (*domain.Link, error) {
		databaseCalls.Add(1)
		<-releaseDatabase
		return &domain.Link{Code: code, OriginalURL: "https://example.com"}, nil
	}}
	cacheGets := make(chan struct{}, callers*2)
	cache := &fakeLinkCache{
		values: make(map[string]string),
		getHook: func() {
			cacheGets <- struct{}{}
		},
	}
	service := newCachedTestLinkService(store, cache)

	start := make(chan struct{})
	errorsByCaller := make(chan error, callers)
	for caller := 0; caller < callers; caller++ {
		go func() {
			<-start
			_, err := service.Resolve(context.Background(), "popular")
			errorsByCaller <- err
		}()
	}
	close(start)
	for observed := 0; observed < callers; observed++ {
		<-cacheGets
	}
	close(releaseDatabase)

	for caller := 0; caller < callers; caller++ {
		if err := <-errorsByCaller; err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
	}
	if calls := databaseCalls.Load(); calls != 1 {
		t.Fatalf("database calls = %d, want 1", calls)
	}
}

func newCachedTestLinkService(store LinkStore, cache LinkCache) *LinkService {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewLinkServiceWithCache(store, cache, logger, 2*time.Second)
}
