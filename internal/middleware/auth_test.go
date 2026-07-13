package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware([]string{"good-key", "second-key"}, logger)(next)

	tests := []struct {
		name string
		key  string
		want int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "bad-key", http.StatusUnauthorized},
		{"first valid key", "good-key", http.StatusOK},
		{"second valid key", "second-key", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.key != "" {
				req.Header.Set("X-API-Key", tt.key)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestAuthMiddleware_RejectionBodyIsJSON(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware([]string{"good-key"}, logger)(next)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("401 response has an empty body, want a JSON error")
	}
}

func TestValidKey_EmptyKeyNeverMatches(t *testing.T) {
	// Guards against a config bug where an empty string ends up in the valid
	// key list (e.g. a blank line in YAML) accidentally granting access to
	// anyone who sends no key at all.
	if validKey("", []string{"", "real-key"}) {
		t.Error("validKey(\"\", ...) = true, want false even if an empty string is in the valid list")
	}
}
