package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_GeneratesIDWhenAbsent(t *testing.T) {
	var fromContext string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromContext = GetRequestId(r.Context())
	})

	rec := httptest.NewRecorder()
	RequestIDMiddleware(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	headerID := rec.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Fatal("X-Request-ID header not set")
	}
	if fromContext != headerID {
		t.Errorf("request ID in context = %q, want it to match response header %q", fromContext, headerID)
	}
}

func TestRequestIDMiddleware_PreservesIncomingID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-supplied-id")

	rec := httptest.NewRecorder()
	RequestIDMiddleware(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "client-supplied-id" {
		t.Errorf("X-Request-ID = %q, want %q (should preserve client-supplied value)", got, "client-supplied-id")
	}
}

func TestGetRequestId_MissingFromContextReturnsEmpty(t *testing.T) {
	if got := GetRequestId(context.Background()); got != "" {
		t.Errorf("GetRequestId on a bare context = %q, want empty string", got)
	}
}
