package authn

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// echoHandler is a test handler that records the user from context.
func echoHandler(t *testing.T, wantUser string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			t.Fatal("expected user in context")
		}
		if user != wantUser {
			t.Fatalf("got user %q, want %q", user, wantUser)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_ValidHeader(t *testing.T) {
	mw := Middleware(false)
	handler := mw(echoHandler(t, "alice@example.com"))

	payload := `{"email":"alice@example.com"}`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Endpoint-API-UserInfo", encoded)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMiddleware_MissingHeader(t *testing.T) {
	mw := Middleware(false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_MalformedBase64(t *testing.T) {
	mw := Middleware(false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Endpoint-API-UserInfo", "%%%not-base64%%%")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_MissingEmail(t *testing.T) {
	mw := Middleware(false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	payload := `{"sub":"1234567890"}`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Endpoint-API-UserInfo", encoded)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_DevMode_WithHeader(t *testing.T) {
	mw := Middleware(true)
	handler := mw(echoHandler(t, "dev@example.com"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dev-User", "dev@example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMiddleware_DevMode_MissingHeader(t *testing.T) {
	mw := Middleware(true)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
