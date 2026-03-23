package api

import (
	"fmt"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         int
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         86400,
	}
}

func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	maxAge := fmt.Sprintf("%d", cfg.MaxAge)
	allowCredentials := allowsCredentials(cfg.AllowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && isAllowedOrigin(origin, cfg.AllowedOrigins) {
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				if allowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if cfg.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isAllowedOrigin(origin string, allowed []string) bool {
	for _, o := range allowed {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

func allowsCredentials(allowed []string) bool {
	for _, o := range allowed {
		if o == "*" {
			return false
		}
	}
	return len(allowed) > 0
}
