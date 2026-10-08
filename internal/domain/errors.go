package domain

import "errors"

// Sentinel errors returned by repositories and consumed by the HTTP layer so
// that transport code can map them to the right status without knowing about
// the database driver.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLinkNotFound       = errors.New("link not found")

	ErrSlugTooShort = errors.New("slug must be at least 3 characters")
	ErrSlugTooLong  = errors.New("slug must not exceed 64 characters")
	ErrSlugInvalid  = errors.New("slug can only contain alphanumeric characters, dashes, and underscores")
	ErrSlugReserved = errors.New("slug is reserved for system use")

	ErrSlugGenerationFailed = errors.New("failed to generate valid slug after max attempts")
	ErrRandomByteFailed     = errors.New("failed to read secure random bytes")
)
