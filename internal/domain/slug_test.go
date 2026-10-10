package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSlug_Lengths(t *testing.T) {
	tests := []struct {
		name      string
		slug      string
		wantValid bool
		wantErr   error
	}{
		{name: "minimum length", slug: "abc", wantValid: true},
		{name: "maximum length", slug: strings.Repeat("a", MaxSlugLength), wantValid: true},
		{name: "below minimum", slug: "ab", wantErr: ErrSlugTooShort},
		{name: "above maximum", slug: strings.Repeat("a", MaxSlugLength+1), wantErr: ErrSlugTooLong},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valid, err := ValidateSlug(test.slug)
			if valid != test.wantValid {
				t.Fatalf("ValidateSlug(%q) valid = %v, want %v", test.slug, valid, test.wantValid)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateSlug(%q) error = %v, want %v", test.slug, err, test.wantErr)
			}
		})
	}
}

func TestValidateSlug_DisallowedCharacters(t *testing.T) {
	tests := []string{
		"has space",
		"email@example",
		"bang!slug",
		"path/slug",
		"/..",
	}

	for _, slug := range tests {
		t.Run(slug, func(t *testing.T) {
			valid, err := ValidateSlug(slug)
			if valid {
				t.Fatalf("ValidateSlug(%q) valid = true, want false", slug)
			}
			if !errors.Is(err, ErrSlugInvalid) {
				t.Fatalf("ValidateSlug(%q) error = %v, want %v", slug, err, ErrSlugInvalid)
			}
		})
	}
}

func TestValidateSlug_RejectsReservedRoutes(t *testing.T) {
	for reserved := range reservedSlugs {
		for _, slug := range []string{reserved, "/" + reserved} {
			t.Run(slug, func(t *testing.T) {
				valid, err := ValidateSlug(slug)
				if valid {
					t.Fatalf("ValidateSlug(%q) valid = true, want false", slug)
				}
				if !errors.Is(err, ErrSlugReserved) {
					t.Fatalf("ValidateSlug(%q) error = %v, want %v", slug, err, ErrSlugReserved)
				}
			})
		}
	}
}
