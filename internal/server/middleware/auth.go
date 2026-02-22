package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth returns middleware that validates API requests using either a Bearer
// token in the Authorization header or a static key in the X-API-Key header.
// If apiKey is empty, the middleware passes all requests through (disabled).
func Auth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no API key is configured, authentication is disabled.
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			token := extractToken(r)
			if token == "" {
				writeUnauthorized(w, "missing authentication token")
				return
			}

			// Constant-time comparison to prevent timing attacks.
			if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				writeUnauthorized(w, "invalid authentication token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractToken looks for a token in the Authorization header (Bearer scheme)
// or in the X-API-Key header.
func extractToken(r *http.Request) string {
	// Check Authorization: Bearer <token>
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	// Check X-API-Key header.
	if key := r.Header.Get("X-API-Key"); key != "" {
		return strings.TrimSpace(key)
	}

	return ""
}

// writeUnauthorized sends a 401 response with a JSON error body.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}
