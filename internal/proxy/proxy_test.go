package proxy

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/metrics"
)

// testLogger discards output — these tests only assert HTTP behavior, not
// what gets logged, and NewRouteHandler requires a non-nil logger.
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

// TestNewRouteHandler_TimesOutSlowUpstream verifies the route-level timeout
// added on top of httputil.ReverseProxy: a request to an upstream that takes
// longer than route.Timeout must fail fast (well before the upstream would
// have responded) instead of hanging for the upstream's full response time.
func TestNewRouteHandler_TimesOutSlowUpstream(t *testing.T) {
	const upstreamDelay = 300 * time.Millisecond
	const routeTimeout = 30 * time.Millisecond

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(upstreamDelay)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	route := &config.Route{
		PathPrefix: "/slow",
		Strategy:   config.RoundRobin,
		Timeout:    routeTimeout,
		Upstreams:  []config.Upstream{{URL: slow.URL, Weight: 1}},
	}

	handler, _, err := NewRouteHandler(route, testLogger(), metrics.New(), nil)
	if err != nil {
		t.Fatalf("NewRouteHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if elapsed >= upstreamDelay {
		t.Errorf("request took %v, want well under the upstream's %v delay (timeout should have cut it short)", elapsed, upstreamDelay)
	}

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d (ReverseProxy's default ErrorHandler on a cancelled context)", rec.Code, http.StatusBadGateway)
	}
}

// TestNewRouteHandler_FastUpstreamSucceeds guards against the timeout wrapper
// itself breaking the ordinary, well-within-budget request path.
func TestNewRouteHandler_FastUpstreamSucceeds(t *testing.T) {
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}))
	defer fast.Close()

	route := &config.Route{
		PathPrefix: "/fast",
		Strategy:   config.RoundRobin,
		Timeout:    2 * time.Second,
		Upstreams:  []config.Upstream{{URL: fast.URL, Weight: 1}},
	}

	handler, _, err := NewRouteHandler(route, testLogger(), metrics.New(), nil)
	if err != nil {
		t.Fatalf("NewRouteHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fast", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello")
	}
}
