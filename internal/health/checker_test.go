package health

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PortableBaka/gateway/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

// fakeRecorder is a test double for MetricsRecorder that records the last
// value set per route+upstream, so tests can assert the health gauge would
// have been updated without depending on the real metrics package.
type fakeRecorder struct {
	mu     sync.Mutex
	values map[string]bool
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{values: make(map[string]bool)}
}

func (f *fakeRecorder) SetUpstreamHealthy(route, upstream string, healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[route+"|"+upstream] = healthy
}

func (f *fakeRecorder) get(route, upstream string) (value, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok = f.values[route+"|"+upstream]
	return value, ok
}

func TestChecker_IsHealthyDefaultsTrue(t *testing.T) {
	up := &config.Upstream{URL: "http://example.invalid"}
	cfg := config.HealthCheck{HealthyThreshold: 2, UnhealthyThreshold: 2}
	c := NewChecker([]*config.Upstream{up}, cfg, testLogger(), "/x", nil)

	if !c.IsHealthy(up) {
		t.Error("IsHealthy() = false immediately after construction, want true (optimistic default)")
	}
}

func TestChecker_IsHealthyUnknownUpstreamFailsOpen(t *testing.T) {
	c := NewChecker(nil, config.HealthCheck{HealthyThreshold: 1, UnhealthyThreshold: 1}, testLogger(), "/x", nil)
	unknown := &config.Upstream{URL: "http://untracked"}

	if !c.IsHealthy(unknown) {
		t.Error("IsHealthy() for an untracked upstream = false, want true (fail open)")
	}
}

func TestChecker_MarksUnhealthyAfterThresholdFailures(t *testing.T) {
	up := &config.Upstream{URL: "http://x"}
	rec := newFakeRecorder()
	cfg := config.HealthCheck{HealthyThreshold: 2, UnhealthyThreshold: 2}
	c := NewChecker([]*config.Upstream{up}, cfg, testLogger(), "/route", rec)

	c.RecordFailure(up)
	if !c.IsHealthy(up) {
		t.Fatal("IsHealthy() = false after 1 failure, want true: unhealthy threshold is 2")
	}

	c.RecordFailure(up)
	if c.IsHealthy(up) {
		t.Fatal("IsHealthy() = true after 2 failures, want false: threshold reached")
	}

	if v, ok := rec.get("/route", "http://x"); !ok || v {
		t.Errorf("metrics recorder value = (%v, ok=%v), want (false, true)", v, ok)
	}
}

func TestChecker_RecoversAfterThresholdSuccesses(t *testing.T) {
	up := &config.Upstream{URL: "http://x"}
	cfg := config.HealthCheck{HealthyThreshold: 2, UnhealthyThreshold: 1}
	c := NewChecker([]*config.Upstream{up}, cfg, testLogger(), "/route", nil)

	c.RecordFailure(up) // unhealthy immediately, threshold is 1
	if c.IsHealthy(up) {
		t.Fatal("expected unhealthy after 1 failure (unhealthy_threshold=1)")
	}

	c.RecordSuccess(up)
	if c.IsHealthy(up) {
		t.Fatal("IsHealthy() = true after 1 success, want false: healthy threshold is 2")
	}

	c.RecordSuccess(up)
	if !c.IsHealthy(up) {
		t.Fatal("IsHealthy() = false after 2 successes, want true: healthy threshold reached")
	}
}

func TestChecker_RecordFailureOnUnknownUpstreamDoesNotPanic(t *testing.T) {
	c := NewChecker(nil, config.HealthCheck{HealthyThreshold: 1, UnhealthyThreshold: 1}, testLogger(), "/x", newFakeRecorder())
	unknown := &config.Upstream{URL: "http://untracked"}

	c.RecordFailure(unknown)
	c.RecordSuccess(unknown)
}

// TestChecker_RunProbesActivelyAndDetectsFailure exercises the actual
// background-goroutine path (Run + probe), not just the RecordSuccess/
// RecordFailure bookkeeping — a real httptest.Server flips from healthy to
// failing mid-test, and the checker must catch that on its own via active
// probing, with no request traffic involved at all.
func TestChecker_RunProbesActivelyAndDetectsFailure(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	up := &config.Upstream{URL: srv.URL}
	cfg := config.HealthCheck{
		Path:               "/",
		Interval:           10 * time.Millisecond,
		Timeout:            time.Second,
		HealthyThreshold:   1,
		UnhealthyThreshold: 2,
	}
	c := NewChecker([]*config.Upstream{up}, cfg, testLogger(), "/route", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	time.Sleep(30 * time.Millisecond)
	if !c.IsHealthy(up) {
		t.Fatal("expected upstream to remain healthy while it's responding 200")
	}

	healthy.Store(false)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !c.IsHealthy(up) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected upstream to be marked unhealthy after active probes started failing")
}

func TestChecker_RunStopsOnContextCancellation(t *testing.T) {
	up := &config.Upstream{URL: "http://127.0.0.1:1"} // nothing listening; probes just fail fast
	cfg := config.HealthCheck{Path: "/", Interval: 5 * time.Millisecond, Timeout: 50 * time.Millisecond, HealthyThreshold: 1, UnhealthyThreshold: 1}
	c := NewChecker([]*config.Upstream{up}, cfg, testLogger(), "/route", nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of context cancellation")
	}
}
