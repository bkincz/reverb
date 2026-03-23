package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/internal/roles"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type contextKey int

const claimsKey contextKey = iota

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func RequireAuth(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := bearerToken(r)
			if tokenStr == "" {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or malformed authorization header")
				return
			}

			claims, err := VerifyAccess(cfg.Tokens, tokenStr)
			if err != nil {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
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
			tokenStr := bearerToken(r)
			if tokenStr == "" {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := VerifyAccess(cfg.Tokens, tokenStr)
			if err != nil {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
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

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}
