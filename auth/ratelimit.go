package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bkincz/reverb/api"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type bucket struct {
	tokens    float64
	lastRefil time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64
	capacity float64
}

// ---------------------------------------------------------------------------
// Rate limiter
// ---------------------------------------------------------------------------

func NewRateLimiter(requestsPerMinute int) func(http.Handler) http.Handler {
	capacity := float64(requestsPerMinute)
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     capacity / 60.0,
		capacity: capacity,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			if !rl.allow(ip) {
				retryAfter := int(60.0 / rl.rate)
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[ip]
	if !exists {
		rl.buckets[ip] = &bucket{tokens: rl.capacity - 1, lastRefil: now}
		return true
	}

	elapsed := now.Sub(b.lastRefil).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.lastRefil = now

	if b.tokens >= rl.capacity {
		delete(rl.buckets, ip)
		return true
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens--
	return true
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// TODO: add TrustedProxy config to AuthConfig to enable X-Forwarded-For
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
