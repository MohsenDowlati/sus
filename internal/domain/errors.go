package domain

import "errors"

// Sentinel errors returned by repositories and consumed by the HTTP layer so
// that transport code can map them to the right status without knowing about
// the database driver.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrInvalidCredentials = errors.New("invalid credentials")
)
