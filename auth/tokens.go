package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TokenConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// ---------------------------------------------------------------------------
// Token functions
// ---------------------------------------------------------------------------

func SignAccess(cfg TokenConfig, userID, email, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(cfg.AccessTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(cfg.Secret))
	if err != nil {
		return "", fmt.Errorf("auth: sign access token: %w", err)
	}
	return signed, nil
}

func VerifyAccess(cfg TokenConfig, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(cfg.Secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: verify access token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: invalid token claims")
	}

	for _, aud := range claims.Audience {
		if aud == sseTicketAudience {
			return nil, fmt.Errorf("auth: SSE tickets cannot be used as access tokens")
		}
	}

	return claims, nil
}

func GenerateRefresh() (raw string, hashed string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hashed = hex.EncodeToString(sum[:])
	return raw, hashed, nil
}

func HashRefresh(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

const sseTicketAudience = "reverb:sse"

func SignSSETicket(cfg TokenConfig, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Audience:  jwt.ClaimStrings{sseTicketAudience},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(cfg.Secret))
	if err != nil {
		return "", fmt.Errorf("auth: sign sse ticket: %w", err)
	}
	return signed, nil
}

func VerifySSETicket(cfg TokenConfig, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(cfg.Secret), nil
		},
		jwt.WithAudience(sseTicketAudience),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: verify sse ticket: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: invalid sse ticket claims")
	}
	return claims, nil
}
