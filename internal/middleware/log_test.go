package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogMiddleware_CapturesStatusPathAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	// RequestIDMiddleware outermost so LogMiddleware can read the ID back,
	// same composition main.go uses.
	handler := Chain(next, RequestIDMiddleware, LogMiddleware(logger))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brew", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v (raw: %s)", err, buf.String())
	}

	if got := line["status"]; got != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want %d", got, http.StatusTeapot)
	}
	if line["path"] != "/brew" {
		t.Errorf("path = %v, want /brew", line["path"])
	}
	wantID := rec.Header().Get("X-Request-ID")
	if line["request_id"] != wantID {
		t.Errorf("request_id in log = %v, want %v (response header)", line["request_id"], wantID)
	}
}

// TestLogMiddleware_DefaultsStatusToOK guards the statusRecorder's default:
// if the handler never calls WriteHeader explicitly, net/http itself
// defaults to 200 — the wrapper must report the same thing, not 0.
func TestLogMiddleware_DefaultsStatusToOK(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("no explicit WriteHeader call"))
	})

	handler := LogMiddleware(logger)(next)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v (raw: %s)", err, buf.String())
	}
	if got := line["status"]; got != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d", got, http.StatusOK)
	}
}
