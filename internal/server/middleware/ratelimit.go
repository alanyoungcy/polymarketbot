package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// RateLimit returns middleware that applies per-client rate limiting using the
// provided domain.RateLimiter. Each unique client IP is limited to `limit`
// requests per `window` duration.
func RateLimit(limiter domain.RateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := extractClientIP(r)
			key := "ratelimit:api:" + clientIP

			allowed, err := limiter.Allow(context.Background(), key, limit, window)
			if err != nil {
				// On rate-limiter errors, fail open to avoid blocking
				// legitimate traffic. The error is not surfaced to the client.
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractClientIP attempts to determine the real client IP from standard
// proxy headers, falling back to the direct remote address.
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (may contain multiple IPs).
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}

	// Check X-Real-IP.
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
