package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

// HMACAuth holds the credentials required for HMAC-authenticated requests
// against the Polymarket CLOB and Builder APIs.
type HMACAuth struct {
	Key        string // API key
	Secret     string // API secret (base64-encoded for L2, raw for Builder)
	Passphrase string // API passphrase
}

// BuilderHeaders returns the HTTP headers for a Builder API request.
// The signature is HMAC-SHA256(secret, timestamp+method+path+body) encoded
// as base64.
//
// Returned header keys:
//   - POLY_BUILDER_API_KEY
//   - POLY_BUILDER_TIMESTAMP
//   - POLY_BUILDER_PASSPHRASE
//   - POLY_BUILDER_SIGNATURE
func (h *HMACAuth) BuilderHeaders(method, path, body string) map[string]string {
	ts := currentTimestamp()

	message := ts + method + path + body
	sig := hmacSHA256Base64([]byte(h.Secret), message)

	return map[string]string{
		"POLY_BUILDER_API_KEY":    h.Key,
		"POLY_BUILDER_TIMESTAMP":  ts,
		"POLY_BUILDER_PASSPHRASE": h.Passphrase,
		"POLY_BUILDER_SIGNATURE":  sig,
	}
}

// L2Headers returns the HTTP headers for an L2 (CLOB) API request.
// The secret is first base64-decoded before being used as the HMAC key.
//
// Returned header keys:
//   - POLY_ADDRESS
//   - POLY_API_KEY
//   - POLY_TIMESTAMP
//   - POLY_PASSPHRASE
//   - POLY_SIGNATURE
func (h *HMACAuth) L2Headers(address, method, path, body string) map[string]string {
	ts := currentTimestamp()

	secretBytes, err := base64.StdEncoding.DecodeString(h.Secret)
	if err != nil {
		// If decoding fails, fall back to raw bytes so the caller gets an
		// obviously-wrong signature rather than a panic.
		secretBytes = []byte(h.Secret)
	}

	message := ts + method + path + body
	sig := hmacSHA256Base64(secretBytes, message)

	return map[string]string{
		"POLY_ADDRESS":    address,
		"POLY_API_KEY":    h.Key,
		"POLY_TIMESTAMP":  ts,
		"POLY_PASSPHRASE": h.Passphrase,
		"POLY_SIGNATURE":  sig,
	}
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// hmacSHA256Base64 computes HMAC-SHA256 of message using key and returns the
// result as a base64 standard-encoded string.
func hmacSHA256Base64(key []byte, message string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// currentTimestamp returns the current Unix epoch time as a decimal string.
func currentTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// BuilderHeadersAt is like BuilderHeaders but lets the caller supply the
// Unix timestamp (useful for deterministic testing).
func (h *HMACAuth) BuilderHeadersAt(method, path, body string, unixTS int64) map[string]string {
	ts := strconv.FormatInt(unixTS, 10)

	message := ts + method + path + body
	sig := hmacSHA256Base64([]byte(h.Secret), message)

	return map[string]string{
		"POLY_BUILDER_API_KEY":    h.Key,
		"POLY_BUILDER_TIMESTAMP":  ts,
		"POLY_BUILDER_PASSPHRASE": h.Passphrase,
		"POLY_BUILDER_SIGNATURE":  sig,
	}
}

// L2HeadersAt is like L2Headers but lets the caller supply the Unix
// timestamp (useful for deterministic testing).
func (h *HMACAuth) L2HeadersAt(address, method, path, body string, unixTS int64) map[string]string {
	ts := strconv.FormatInt(unixTS, 10)

	secretBytes, err := base64.StdEncoding.DecodeString(h.Secret)
	if err != nil {
		secretBytes = []byte(h.Secret)
	}

	message := ts + method + path + body
	sig := hmacSHA256Base64(secretBytes, message)

	return map[string]string{
		"POLY_ADDRESS":    address,
		"POLY_API_KEY":    h.Key,
		"POLY_TIMESTAMP":  ts,
		"POLY_PASSPHRASE": h.Passphrase,
		"POLY_SIGNATURE":  sig,
	}
}

// String returns a redacted representation suitable for logging.
func (h *HMACAuth) String() string {
	redact := func(s string) string {
		if len(s) <= 4 {
			return "****"
		}
		return s[:4] + "****"
	}
	return fmt.Sprintf("HMACAuth{key=%s, secret=%s}", redact(h.Key), redact(h.Secret))
}
