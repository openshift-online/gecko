package authn

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

const (
	// headerUserInfo is the header set by ESPv2 containing base64-encoded
	// JWT claims about the authenticated user.
	headerUserInfo = "X-Endpoint-API-UserInfo"

	// headerDevUser is used in dev mode to pass the user email directly.
	headerDevUser = "X-Dev-User"
)

// Middleware returns HTTP middleware that extracts the authenticated user
// identity and injects it into the request context.
//
// In normal mode, it decodes the X-Endpoint-API-UserInfo header (base64 JSON)
// and extracts the "email" claim. Missing or malformed headers result in 401.
//
// In dev mode (disableAuth=true), it reads the X-Dev-User header directly.
// Missing header results in 401.
func Middleware(disableAuth bool) func(http.Handler) http.Handler {
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
	ctx := WithUser(r.Context(), devUser)
	next.ServeHTTP(w, r.WithContext(ctx))
}

func handleNormalMode(w http.ResponseWriter, r *http.Request, next http.Handler) {
	encoded := r.Header.Get(headerUserInfo)
	if encoded == "" {
		http.Error(w, "missing X-Endpoint-API-UserInfo header", http.StatusUnauthorized)
		return
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		http.Error(w, "malformed X-Endpoint-API-UserInfo header", http.StatusUnauthorized)
		return
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

	ctx := WithUser(r.Context(), claims.Email)
	next.ServeHTTP(w, r.WithContext(ctx))
}
