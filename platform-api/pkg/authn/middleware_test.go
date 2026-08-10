package authn

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_ValidHeader(t *testing.T) {
	claims := map[string]string{"email": "alice@example.com"}
	headerValue := encodeHeader(t, claims)

	var gotEmail string
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, headerValue)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}
	if gotEmail != "alice@example.com" {
		t.Fatalf("Expected email alice@example.com, got %q", gotEmail)
	}
}

func TestMiddleware_ValidHeader_URLSafe(t *testing.T) {
	// Use URL-safe base64 encoding (no padding)
	claims := `{"email":"bob@example.com"}`
	headerValue := base64.RawURLEncoding.EncodeToString([]byte(claims))

	var gotEmail string
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, headerValue)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}
	if gotEmail != "bob@example.com" {
		t.Fatalf("Expected email bob@example.com, got %q", gotEmail)
	}
}

func TestMiddleware_MissingHeader(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_InvalidBase64(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, "!!!not-valid-base64!!!")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_InvalidJSON(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, base64.StdEncoding.EncodeToString([]byte("not json")))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_MissingEmail(t *testing.T) {
	claims := map[string]string{"sub": "user123"}
	headerValue := encodeHeader(t, claims)

	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, headerValue)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_EmptyEmail(t *testing.T) {
	claims := map[string]string{"email": ""}
	headerValue := encodeHeader(t, claims)

	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(UserInfoHeader, headerValue)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401, got %d", rr.Code)
	}
}

func TestUserFromContext_NoUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	email := UserFromContext(req.Context())
	if email != "" {
		t.Fatalf("Expected empty email, got %q", email)
	}
}

func TestContextWithUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := ContextWithUser(req.Context(), "test@example.com")
	email := UserFromContext(ctx)
	if email != "test@example.com" {
		t.Fatalf("Expected test@example.com, got %q", email)
	}
}

func TestDevModeMiddleware(t *testing.T) {
	t.Run("with_header", func(t *testing.T) {
		var gotEmail string
		handler := DevModeMiddleware("default@dev.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotEmail = UserFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set(DevUserHeader, "custom@dev.com")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if gotEmail != "custom@dev.com" {
			t.Fatalf("Expected custom@dev.com, got %q", gotEmail)
		}
	})

	t.Run("without_header_uses_default", func(t *testing.T) {
		var gotEmail string
		handler := DevModeMiddleware("default@dev.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotEmail = UserFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if gotEmail != "default@dev.com" {
			t.Fatalf("Expected default@dev.com, got %q", gotEmail)
		}
	})

	t.Run("without_header_or_default", func(t *testing.T) {
		var gotEmail string
		handler := DevModeMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotEmail = UserFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if gotEmail != "dev@localhost" {
			t.Fatalf("Expected dev@localhost, got %q", gotEmail)
		}
	})
}

func encodeHeader(t *testing.T, claims interface{}) string {
	t.Helper()
	data, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Failed to marshal claims: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
