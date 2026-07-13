package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurstThenRejects(t *testing.T) {
	rl := NewRateLimiter(1, 3) // 1 req/s, burst of 3

	allowed := 0
	for i := 0; i < 5; i++ {
		if rl.getLimiter("1.2.3.4").Allow() {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed = %d, want 3 (the configured burst)", allowed)
	}
}

func TestRateLimiter_PerKeyIsolation(t *testing.T) {
	rl := NewRateLimiter(1, 1)

	if !rl.getLimiter("client-a").Allow() {
		t.Fatal("client-a's first request should be allowed")
	}
	if rl.getLimiter("client-a").Allow() {
		t.Fatal("client-a's second immediate request should be rejected: burst is 1")
	}
	if !rl.getLimiter("client-b").Allow() {
		t.Fatal("client-b should have its own independent bucket, unaffected by client-a")
	}
}

func TestRateLimiter_CleanupStaleEvictsOldEntries(t *testing.T) {
	rl := NewRateLimiter(10, 10)
	rl.getLimiter("stale-client")

	if len(rl.visitors) != 1 {
		t.Fatalf("visitors = %d, want 1 before cleanup", len(rl.visitors))
	}

	// maxAge 0: anything not seen in the future (i.e. everything) is stale.
	rl.CleanupStale(0)

	if len(rl.visitors) != 0 {
		t.Errorf("visitors = %d after cleanup, want 0", len(rl.visitors))
	}
}

func TestRateLimiter_CleanupStaleKeepsRecentEntries(t *testing.T) {
	rl := NewRateLimiter(10, 10)
	rl.getLimiter("fresh-client")

	rl.CleanupStale(time.Hour)

	if len(rl.visitors) != 1 {
		t.Errorf("visitors = %d after cleanup with a long maxAge, want 1 (should not evict a fresh entry)", len(rl.visitors))
	}
}

func TestRateLimitMiddleware_RejectsWithTooManyRequests(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RateLimitMiddleware(rl, logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:1234"

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second immediate request status = %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

func TestClientKey_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54321"

	if got := clientKey(req); got != "203.0.113.5" {
		t.Errorf("clientKey = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientKey_FallsBackToRawRemoteAddrWithoutPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-host-port"

	if got := clientKey(req); got != "not-a-host-port" {
		t.Errorf("clientKey = %q, want the raw RemoteAddr as a fallback", got)
	}
}
