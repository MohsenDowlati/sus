package domain

import (
	"crypto/rand"
	"regexp"
	"strings"
)

const (
	MinSlugLength   = 3
	MaxSlugLength   = 64
	slugLength      = 7
	maxSlugAttempts = 3
	base62Alphabet  = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	maxUnbiasedByte = 248
)

var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var reservedSlugs = map[string]struct{}{
	"api": {}, "v1": {}, "v2": {}, "healthz": {}, "readyz": {},
	"metrics": {}, "login": {}, "register": {}, "auth": {},
	"dashboard": {}, "admin": {}, "docs": {}, "swagger": {},
	"static": {}, "favicon.ico": {}, "robots.txt": {},
}

func ValidateSlug(slug string) (bool, error) {
	slug = strings.TrimSpace(slug)
	if IsReservedSlug(slug) {
		return false, ErrSlugReserved
	}
	if len(slug) < MinSlugLength {
		return false, ErrSlugTooShort
	}
	if len(slug) > MaxSlugLength {
		return false, ErrSlugTooLong
	}
	if !validSlugPattern.MatchString(slug) {
		return false, ErrSlugInvalid
	}
	return true, nil
}

func IsReservedSlug(slug string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(slug)), "/")
	_, exists := reservedSlugs[normalized]
	return exists
}

func AutoGenerateSlug() (string, error) {
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		slug, err := generateSecureSlug()
		if err != nil {
			return "", err
		}
		if valid, err := ValidateSlug(slug); err == nil && valid {
			return slug, nil
		}
	}
	return "", ErrSlugGenerationFailed
}

func generateSecureSlug() (string, error) {
	result := make([]byte, slugLength)
	randomBytes := make([]byte, slugLength+5)
	generated := 0
	for generated < slugLength {
		if _, err := rand.Read(randomBytes); err != nil {
			return "", ErrRandomByteFailed
		}
		for _, randomByte := range randomBytes {
			character, accepted := base62Character(randomByte)
			if !accepted {
				continue
			}
			result[generated] = character
			generated++
			if generated == slugLength {
				return string(result), nil
			}
		}
	}
	return string(result), nil
}

func base62Character(randomByte byte) (byte, bool) {
	if randomByte >= maxUnbiasedByte {
		return 0, false
	}
	return base62Alphabet[randomByte%byte(len(base62Alphabet))], true
}
