package domain

import (
	"strings"
	"testing"
)

func TestGenerateSecureSlug_Base62AndLength(t *testing.T) {
	for attempt := 0; attempt < 1_000; attempt++ {
		slug, err := generateSecureSlug()
		if err != nil {
			t.Fatalf("generateSecureSlug() error = %v", err)
		}
		if len(slug) != slugLength {
			t.Fatalf("generateSecureSlug() length = %d, want %d", len(slug), slugLength)
		}
		for _, character := range slug {
			if !strings.ContainsRune(base62Alphabet, character) {
				t.Fatalf("generateSecureSlug() returned non-Base62 character %q in %q", character, slug)
			}
		}
	}
}

func TestBase62Character_UsesUniformRejectionSampling(t *testing.T) {
	counts := make(map[byte]int, len(base62Alphabet))
	rejected := 0
	for value := 0; value <= 255; value++ {
		character, accepted := base62Character(byte(value))
		if !accepted {
			rejected++
			continue
		}
		counts[character]++
	}

	if rejected != 256-maxUnbiasedByte {
		t.Fatalf("rejected byte count = %d, want %d", rejected, 256-maxUnbiasedByte)
	}
	for index := range base62Alphabet {
		character := base62Alphabet[index]
		if counts[character] != 4 {
			t.Fatalf("character %q mapped %d times, want 4", character, counts[character])
		}
	}
}
