// Package authn provides authentication middleware for the public API.
//
// It extracts user identity from the X-Endpoint-API-UserInfo header injected
// by ESPv2 (base64-encoded JWT claims) and stores the user email in the
// request context.
package authn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// UserInfoHeader is the header name injected by ESPv2 with base64-encoded JWT claims.
const UserInfoHeader = "X-Endpoint-API-UserInfo"

// DevUserHeader is the header name used in dev mode (when --disable-auth is set)
// to inject a user identity without ESPv2.
const DevUserHeader = "X-Dev-User"

// contextKey is an unexported type for context keys to prevent collisions.
type contextKey struct{}

// userEmailKey is the context key for the authenticated user's email.
var userEmailKey = contextKey{}

// UserFromContext retrieves the authenticated user's email from the request context.
// Returns an empty string if no user is set.
func UserFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userEmailKey).(string)
	return v
}

// ContextWithUser returns a new context with the user email set.
// Exported for use in tests and dev mode bypass.
func ContextWithUser(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, userEmailKey, email)
}

// jwtClaims represents the subset of JWT claims we extract from the UserInfo header.
type jwtClaims struct {
	Email string `json:"email"`
}

// Middleware returns HTTP middleware that extracts user identity from the
// X-Endpoint-API-UserInfo header. If the header is missing or malformed,
// it returns 401 Unauthenticated.
//
// Health and readiness endpoints (/healthz, /readyz) are exempt from
// authentication and pass through without the header.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health/readiness probes
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		headerValue := r.Header.Get(UserInfoHeader)
		if headerValue == "" {
			writeError(w, http.StatusUnauthorized, "Unauthenticated: missing "+UserInfoHeader+" header")
			return
		}

		// ESPv2 base64-encodes the JWT claims (may use standard or URL-safe encoding,
		// with or without padding).
		decoded, err := base64Decode(headerValue)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Unauthenticated: invalid base64 in "+UserInfoHeader+" header")
			return
		}

		var claims jwtClaims
		if err := json.Unmarshal(decoded, &claims); err != nil {
			writeError(w, http.StatusUnauthorized, "Unauthenticated: invalid JSON in "+UserInfoHeader+" header")
			return
		}

		if claims.Email == "" {
			writeError(w, http.StatusUnauthorized, "Unauthenticated: missing email claim in "+UserInfoHeader+" header")
			return
		}

		ctx := ContextWithUser(r.Context(), claims.Email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DevModeMiddleware returns HTTP middleware that bypasses ESPv2 authentication
// for local development. It reads the user email from the X-Dev-User header
// or falls back to a default email.
func DevModeMiddleware(defaultEmail string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			email := r.Header.Get(DevUserHeader)
			if email == "" {
				email = defaultEmail
			}
			if email == "" {
				email = "dev@localhost"
			}
			ctx := ContextWithUser(r.Context(), email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// base64Decode tries standard encoding first, then URL-safe encoding,
// with and without padding.
func base64Decode(s string) ([]byte, error) {
	// Try standard encoding with padding
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try standard encoding without padding (raw)
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try URL-safe encoding with padding
	if decoded, err := base64.URLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	// Try URL-safe encoding without padding (raw)
	return base64.RawURLEncoding.DecodeString(s)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":    "Status",
		"status":  "Failure",
		"message": message,
		"code":    code,
	})
}
