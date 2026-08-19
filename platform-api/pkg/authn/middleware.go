package authn

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"golang.org/x/text/unicode/norm"
)

const (
	// headerUserInfo is the header set by ESPv2 containing base64-encoded
	// JWT claims about the authenticated user.
	headerUserInfo = "X-Endpoint-API-UserInfo"

	// headerDevUser is used in dev mode to pass the user email directly.
	headerDevUser = "X-Dev-User"
)

// normalizeEmail normalizes an email address for consistent Cedar principal matching.
// Applies NFC unicode normalization and lowercases the domain part.
func normalizeEmail(email string) string {
	// Apply NFC normalization to the entire email.
	email = norm.NFC.String(email)

	// Lowercase the domain part (after @).
	if parts := strings.Split(email, "@"); len(parts) == 2 {
		return parts[0] + "@" + strings.ToLower(parts[1])
	}
	return email
}

// Middleware returns HTTP middleware that extracts the authenticated user
// identity and injects it into the request context.
//
// In normal mode, it decodes the X-Endpoint-API-UserInfo header (base64 JSON)
// and extracts the "email" claim. Missing or malformed headers result in 401.
//
// In dev mode (disableAuth=true), it reads the X-Dev-User header directly
// without any verification. This mode is for LOCAL DEVELOPMENT ONLY and should
// never be enabled in production or untrusted environments. Missing header
// results in 401.
func Middleware(disableAuth bool) func(http.Handler) http.Handler {
	if disableAuth {
		log.Println("WARNING: Authentication is disabled. This mode is for local development only. " +
			"Do not enable in production or untrusted environments.")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if disableAuth {
				handleDevMode(w, r, next)
				return
			}
			handleNormalMode(w, r, next)
		})
	}
}

func handleDevMode(w http.ResponseWriter, r *http.Request, next http.Handler) {
	devUser := r.Header.Get(headerDevUser)
	if devUser == "" {
		http.Error(w, "missing X-Dev-User header", http.StatusUnauthorized)
		return
	}
	// Normalize the email for consistent Cedar principal matching.
	normalizedUser := normalizeEmail(devUser)
	ctx := WithUser(r.Context(), normalizedUser)
	next.ServeHTTP(w, r.WithContext(ctx))
}

func handleNormalMode(w http.ResponseWriter, r *http.Request, next http.Handler) {
	encoded := r.Header.Get(headerUserInfo)
	if encoded == "" {
		http.Error(w, "missing X-Endpoint-API-UserInfo header", http.StatusUnauthorized)
		return
	}

	// Try to decode both padded (URLEncoding) and unpadded (RawURLEncoding) base64.
	var data []byte
	var err error
	data, err = base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		// Try padded base64 if unpadded fails.
		data, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			http.Error(w, "malformed X-Endpoint-API-UserInfo header", http.StatusUnauthorized)
			return
		}
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		http.Error(w, "malformed X-Endpoint-API-UserInfo header", http.StatusUnauthorized)
		return
	}

	if claims.Email == "" {
		http.Error(w, "missing email claim in X-Endpoint-API-UserInfo", http.StatusUnauthorized)
		return
	}

	// Normalize the email for consistent Cedar principal matching.
	normalizedEmail := normalizeEmail(claims.Email)
	ctx := WithUser(r.Context(), normalizedEmail)
	next.ServeHTTP(w, r.WithContext(ctx))
}
