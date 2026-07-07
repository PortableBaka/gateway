package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverMiddleware_PassesThroughNormalRequests(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := RecoverMiddleware(logger)(ok)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestRecoverMiddleware_RecoversPanic(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	panics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	handler := RecoverMiddleware(logger)(panics)

	rec := httptest.NewRecorder()

	// The point of this test: ServeHTTP must return normally (not propagate
	// the panic to the test goroutine) and still produce a response.
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestRecoverMiddleware_LogsRequestIDAndStack wires the middleware up the
// same way main.go does — RequestIDMiddleware outermost, RecoverMiddleware
// next — to prove the ordering actually delivers what it's supposed to: the
// panic log line carries the same request ID as the response header, plus a
// stack trace, instead of a generic unlabeled "something broke".
func TestRecoverMiddleware_LogsRequestIDAndStack(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	panics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	handler := Chain(panics, RequestIDMiddleware, RecoverMiddleware(logger))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	wantID := rec.Header().Get("X-Request-ID")
	if wantID == "" {
		t.Fatal("X-Request-ID header not set on response")
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v (raw: %s)", err, buf.String())
	}

	if line["msg"] != "panic recovered" {
		t.Errorf("msg = %v, want %q", line["msg"], "panic recovered")
	}
	if line["error"] != "boom" {
		t.Errorf("error = %v, want %q", line["error"], "boom")
	}
	if line["request_id"] != wantID {
		t.Errorf("request_id in log = %v, want %v (response header)", line["request_id"], wantID)
	}
	if stack, _ := line["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("stack = %q, want it to contain a goroutine dump", stack)
	}
}
