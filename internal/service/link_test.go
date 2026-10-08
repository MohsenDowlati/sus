package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/MohsenDowlati/shorts/internal/domain"
)

type fakeLinkStore struct {
	create func(*domain.Link) error
	codes  []string
}

func (store *fakeLinkStore) Create(_ context.Context, link *domain.Link) error {
	store.codes = append(store.codes, link.Code)
	if store.create != nil {
		return store.create(link)
	}
	return nil
}

func (store *fakeLinkStore) GetByCode(context.Context, string) (*domain.Link, error) {
	return nil, domain.ErrLinkNotFound
}

func TestLinkServiceCreateLink_CustomDuplicate(t *testing.T) {
	store := &fakeLinkStore{create: func(*domain.Link) error {
		return domain.ErrSlugReserved
	}}
	service := newTestLinkService(store)
	code := "existing-code"

	_, err := service.CreateLink(context.Background(), CreateLinkParams{
		OriginalURL: "https://8.8.8.8/article",
		Type:        domain.LinkTypeCustom,
		Code:        &code,
		OwnerID:     "owner-id",
	})

	if !errors.Is(err, domain.ErrSlugReserved) {
		t.Fatalf("CreateLink() error = %v, want %v", err, domain.ErrSlugReserved)
	}
	if len(store.codes) != 1 {
		t.Fatalf("CreateLink() attempts = %d, want 1", len(store.codes))
	}
}

func TestLinkServiceCreateLink_RetriesGeneratedSlugCollision(t *testing.T) {
	store := &fakeLinkStore{}
	createAttempts := 0
	store.create = func(*domain.Link) error {
		createAttempts++
		if createAttempts == 1 {
			return domain.ErrSlugReserved
		}
		return nil
	}
	service := newTestLinkService(store)
	generated := []string{"AAAAAAA", "BBBBBBB"}
	generateCalls := 0
	service.generateSlug = func() (string, error) {
		slug := generated[generateCalls]
		generateCalls++
		return slug, nil
	}

	link, err := service.CreateLink(context.Background(), autoLinkParams())
	if err != nil {
		t.Fatalf("CreateLink() error = %v", err)
	}
	if link.Code != "BBBBBBB" {
		t.Fatalf("CreateLink() code = %q, want %q", link.Code, "BBBBBBB")
	}
	if createAttempts != 2 || generateCalls != 2 {
		t.Fatalf("CreateLink() create attempts = %d, generator calls = %d, want 2 each", createAttempts, generateCalls)
	}
}

func TestLinkServiceCreateLink_FailsAfterThreeGeneratedSlugCollisions(t *testing.T) {
	store := &fakeLinkStore{create: func(*domain.Link) error {
		return domain.ErrSlugReserved
	}}
	service := newTestLinkService(store)
	generateCalls := 0
	service.generateSlug = func() (string, error) {
		generateCalls++
		return "CCCCCCC", nil
	}

	_, err := service.CreateLink(context.Background(), autoLinkParams())
	if !errors.Is(err, domain.ErrSlugGenerationFailed) {
		t.Fatalf("CreateLink() error = %v, want %v", err, domain.ErrSlugGenerationFailed)
	}
	if len(store.codes) != maxAutoSlugAttempts || generateCalls != maxAutoSlugAttempts {
		t.Fatalf("CreateLink() attempts = %d/%d, want %d", len(store.codes), generateCalls, maxAutoSlugAttempts)
	}
}

func TestLinkServiceCreateLink_RejectsBlockedTargetBeforePersistence(t *testing.T) {
	store := &fakeLinkStore{}
	service := newTestLinkService(store)

	_, err := service.CreateLink(context.Background(), CreateLinkParams{
		OriginalURL: "http://169.254.169.254/latest/meta-data",
		Type:        domain.LinkTypeAuto,
		OwnerID:     "owner-id",
	})
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("CreateLink() error = %v, want %v", err, ErrInvalidURL)
	}
	if len(store.codes) != 0 {
		t.Fatalf("repository writes = %d, want 0", len(store.codes))
	}
}

func TestLinkServiceCheckSlug(t *testing.T) {
	tests := []struct {
		name          string
		slug          string
		lookup        func(string) (*domain.Link, error)
		wantAvailable bool
		wantErr       error
	}{
		{
			name: "available",
			slug: "available-slug",
			lookup: func(string) (*domain.Link, error) {
				return nil, domain.ErrLinkNotFound
			},
			wantAvailable: true,
		},
		{
			name: "already used",
			slug: "existing-slug",
			lookup: func(code string) (*domain.Link, error) {
				return &domain.Link{Code: code}, nil
			},
		},
		{
			name:    "invalid",
			slug:    "invalid slug",
			lookup:  func(string) (*domain.Link, error) { return nil, nil },
			wantErr: ErrInvalidSlug,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &cacheTestStore{get: test.lookup}
			service := newTestLinkService(store)
			available, err := service.CheckSlug(context.Background(), test.slug)
			if available != test.wantAvailable {
				t.Fatalf("CheckSlug() available = %v, want %v", available, test.wantAvailable)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CheckSlug() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func newTestLinkService(store LinkStore) *LinkService {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewLinkService(store, logger, time.Second)
}

func autoLinkParams() CreateLinkParams {
	return CreateLinkParams{
		OriginalURL: "https://8.8.8.8/article",
		Type:        domain.LinkTypeAuto,
		OwnerID:     "owner-id",
	}
}
