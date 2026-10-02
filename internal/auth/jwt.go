package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload. Subject (sub) holds the user id; Username is carried
// so protected handlers and token refresh don't need an extra database lookup.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// TokenService issues and verifies HS256 access/refresh tokens. Access and
// refresh tokens are signed with separate secrets so a leaked access token can
// never be used to mint new ones.
type TokenService struct {
	accessSecret  []byte
	refreshSecret []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
}

func NewTokenService(accessSecret, refreshSecret string, accessTTL, refreshTTL time.Duration) *TokenService {
	return &TokenService{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
	}
}

func (s *TokenService) GenerateAccess(userID, username string) (string, error) {
	return s.sign(s.accessSecret, s.accessTTL, userID, username)
}

func (s *TokenService) GenerateRefresh(userID, username string) (string, error) {
	return s.sign(s.refreshSecret, s.refreshTTL, userID, username)
}

func (s *TokenService) ParseAccess(token string) (*Claims, error) {
	return parse(token, s.accessSecret)
}

func (s *TokenService) ParseRefresh(token string) (*Claims, error) {
	return parse(token, s.refreshSecret)
}

func (s *TokenService) sign(secret []byte, ttl time.Duration, userID, username string) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

func parse(tokenStr string, secret []byte) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		// Reject tokens signed with anything but HMAC to prevent algorithm
		// confusion (e.g. a token forged with alg=none or an RS256 key swap).
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
