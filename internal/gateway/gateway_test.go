package gateway

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

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

// TestNew_RoutesToCorrectUpstreamByPrefix is the end-to-end check that ties
// together everything the earlier stages built: given real config.Routes,
// New must build a router that sends each path prefix to the right
// upstream, including bare-prefix and nested-path requests.
func TestNew_RoutesToCorrectUpstreamByPrefix(t *testing.T) {
	usersUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users-backend"))
	}))
	defer usersUp.Close()

	ordersUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("orders-backend"))
	}))
	defer ordersUp.Close()

	cfg := &config.Config{
		Routes: []config.Route{
			{
				PathPrefix: "/users",
				Strategy:   config.RoundRobin,
				Timeout:    time.Second,
				Upstreams:  []config.Upstream{{URL: usersUp.URL, Weight: 1}},
			},
			{
				PathPrefix: "/orders",
				Strategy:   config.RoundRobin,
				Timeout:    time.Second,
				Upstreams:  []config.Upstream{{URL: ordersUp.URL, Weight: 1}},
			},
		},
	}

	handler, checkers, err := New(cfg, testLogger(), metrics.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(checkers) != 2 {
		t.Fatalf("len(checkers) = %d, want 2 (one per route)", len(checkers))
	}

	tests := []struct {
		path string
		want string
	}{
		{"/users", "users-backend"},
		{"/users/", "users-backend"},
		{"/users/42", "users-backend"},
		{"/orders", "orders-backend"},
		{"/orders/7", "orders-backend"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Body.String() != tt.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestNew_UnmatchedPathReturns404(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.Route{
			{
				PathPrefix: "/users",
				Strategy:   config.RoundRobin,
				Timeout:    time.Second,
				Upstreams:  []config.Upstream{{URL: "http://127.0.0.1:1", Weight: 1}},
			},
		},
	}

	handler, _, err := New(cfg, testLogger(), metrics.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestNew_AuthRequiredGatesOnlyThatRoute confirms the Stage 9 design choice:
// auth is wired per-route, built once, and a route with auth.required=false
// (implicitly, by omission) is unaffected by another route requiring it.
func TestNew_AuthRequiredGatesOnlyThatRoute(t *testing.T) {
	secureUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer secureUp.Close()

	openUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer openUp.Close()

	cfg := &config.Config{
		Server: config.Server{APIKeys: []string{"secret"}},
		Routes: []config.Route{
			{
				PathPrefix: "/secure",
				Strategy:   config.RoundRobin,
				Timeout:    time.Second,
				Auth:       config.Auth{Required: true},
				Upstreams:  []config.Upstream{{URL: secureUp.URL, Weight: 1}},
			},
			{
				PathPrefix: "/open",
				Strategy:   config.RoundRobin,
				Timeout:    time.Second,
				Upstreams:  []config.Upstream{{URL: openUp.URL, Weight: 1}},
			},
		},
	}

	handler, _, err := New(cfg, testLogger(), metrics.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/secure", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/secure without key: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("X-API-Key", "secret")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("/secure with valid key: status = %d, want %d", rec2.Code, http.StatusOK)
	}

	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/open", nil))
	if rec3.Code != http.StatusOK {
		t.Errorf("/open without any key: status = %d, want %d (auth not required on this route)", rec3.Code, http.StatusOK)
	}
}
