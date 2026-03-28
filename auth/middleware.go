package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/internal/roles"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type contextKey int

const (
	claimsKey contextKey = iota
	sessionKey
)

var ErrMissingAuth = errors.New("auth: missing authentication")

type Session struct {
	Claims    *Claims
	Source    string
	Refreshed bool
}

const (
	SessionSourceHeader = "header"
	SessionSourceCookie = "cookie"
)

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func RequireAuth(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := ResolveSessionWithRefresh(cfg, w, r)
			if errors.Is(err, ErrMissingAuth) {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing authentication")
				return
			}
			if err != nil {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, session.Claims)
			ctx = context.WithValue(ctx, sessionKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(cfg Config, role string) func(http.Handler) http.Handler {
	if !roles.IsValid(role) {
		return func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "invalid required role configuration")
			})
		}
	}

	authMW := RequireAuth(cfg)

	return func(next http.Handler) http.Handler {
		return authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing authentication")
				return
			}

			if !roles.Allowed(claims.Role, role) {
				api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
				return
			}

			next.ServeHTTP(w, r)
		}))
	}
}

func ParseAuth(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := ResolveSessionWithRefresh(cfg, w, r)
			if errors.Is(err, ErrMissingAuth) {
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, session.Claims)
			ctx = context.WithValue(ctx, sessionKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}

func SessionFromContext(ctx context.Context) (*Session, bool) {
	session, ok := ctx.Value(sessionKey).(*Session)
	return session, ok
}

func ResolveSession(cfg Config, r *http.Request) (*Session, error) {
	if tokenStr := BearerToken(r); tokenStr != "" {
		claims, err := VerifyAccess(cfg.Tokens, tokenStr)
		if err != nil {
			return nil, err
		}
		return &Session{Claims: claims, Source: SessionSourceHeader}, nil
	}

	if tokenStr, ok := AccessCookieToken(r, cfg); ok {
		claims, err := VerifyAccess(cfg.Tokens, tokenStr)
		if err != nil {
			return nil, err
		}
		return &Session{Claims: claims, Source: SessionSourceCookie}, nil
	}

	return nil, ErrMissingAuth
}

func ResolveSessionWithRefresh(cfg Config, w http.ResponseWriter, r *http.Request) (*Session, error) {
	if tokenStr := BearerToken(r); tokenStr != "" {
		return ResolveSession(cfg, r)
	}
	if accessCookieName(cfg) == "" {
		return ResolveSession(cfg, r)
	}

	accessToken, hasAccessCookie := AccessCookieToken(r, cfg)
	if hasAccessCookie {
		claims, err := VerifyAccess(cfg.Tokens, accessToken)
		if err == nil {
			return &Session{Claims: claims, Source: SessionSourceCookie}, nil
		}
		if !IsExpiredAccessError(err) {
			return nil, err
		}
	}

	rawRefresh, ok := RefreshCookieToken(r, cfg)
	if !ok {
		if hasAccessCookie {
			_, err := VerifyAccess(cfg.Tokens, accessToken)
			return nil, err
		}
		return nil, ErrMissingAuth
	}

	_, session, err := refreshSession(r.Context(), cfg, w, rawRefresh)
	return session, err
}

func BearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

func AccessCookieToken(r *http.Request, cfg Config) (string, bool) {
	name := accessCookieName(cfg)
	if name == "" {
		return "", false
	}

	cookie, err := r.Cookie(name)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func RefreshCookieToken(r *http.Request, cfg Config) (string, bool) {
	cookie, err := r.Cookie(cookieName(cfg))
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}
