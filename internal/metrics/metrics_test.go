package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveRequest_IncrementsCounterPerLabelCombination(t *testing.T) {
	m := New()

	m.ObserveRequest("/users", "GET", 200, 50*time.Millisecond)

	got := testutil.ToFloat64(m.requestsTotal.WithLabelValues("/users", "GET", "200"))
	if got != 1 {
		t.Errorf("requestsTotal after 1 observation = %v, want 1", got)
	}

	m.ObserveRequest("/users", "GET", 200, 30*time.Millisecond)
	got = testutil.ToFloat64(m.requestsTotal.WithLabelValues("/users", "GET", "200"))
	if got != 2 {
		t.Errorf("requestsTotal after 2nd observation = %v, want 2", got)
	}

	// A different status is a different label combination entirely.
	m.ObserveRequest("/users", "GET", 500, 10*time.Millisecond)
	got = testutil.ToFloat64(m.requestsTotal.WithLabelValues("/users", "GET", "500"))
	if got != 1 {
		t.Errorf("requestsTotal{status=500} = %v, want 1", got)
	}
	got = testutil.ToFloat64(m.requestsTotal.WithLabelValues("/users", "GET", "200"))
	if got != 2 {
		t.Errorf("requestsTotal{status=200} = %v after an unrelated observation, want unchanged 2", got)
	}
}

func TestObserveRequest_RecordsDurationInHistogram(t *testing.T) {
	m := New()

	m.ObserveRequest("/users", "GET", 200, 50*time.Millisecond)

	if count := testutil.CollectAndCount(m.requestDuration); count == 0 {
		t.Error("requestDuration has no series after ObserveRequest")
	}
}

func TestSetUpstreamHealthy_SetsGaugeValue(t *testing.T) {
	m := New()

	m.SetUpstreamHealthy("/users", "http://localhost:9001", true)
	if got := testutil.ToFloat64(m.upstreamHealthy.WithLabelValues("/users", "http://localhost:9001")); got != 1 {
		t.Errorf("gauge = %v, want 1", got)
	}

	m.SetUpstreamHealthy("/users", "http://localhost:9001", false)
	if got := testutil.ToFloat64(m.upstreamHealthy.WithLabelValues("/users", "http://localhost:9001")); got != 0 {
		t.Errorf("gauge = %v, want 0", got)
	}
}

// TestNew_MetricsAppearInRegistryAfterObservation checks the actual wiring
// New() does — MustRegister against m.Registry, not the global default —
// by gathering from that registry and confirming the expected metric names
// show up, the same path promhttp.HandlerFor(m.Registry, ...) uses.
func TestNew_MetricsAppearInRegistryAfterObservation(t *testing.T) {
	m := New()
	m.ObserveRequest("/x", "GET", 200, time.Millisecond)
	m.SetUpstreamHealthy("/x", "http://up", true)

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}

	for _, want := range []string{
		"gateway_http_requests_total",
		"gateway_http_request_duration_seconds",
		"gateway_upstream_healthy",
	} {
		if !names[want] {
			t.Errorf("metric %q not found in registry after observation; got families: %v", want, names)
		}
	}
}
