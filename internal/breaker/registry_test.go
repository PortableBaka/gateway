package breaker

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/PortableBaka/gateway/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

func TestRegistry_UnknownUpstreamFailsOpen(t *testing.T) {
	r := NewRegistry(nil, 3, time.Second, testLogger())

	unknown := &config.Upstream{URL: "http://untracked"}
	if !r.Allow(unknown) {
		t.Error("Allow() for an untracked upstream = false, want true (fail open)")
	}

	// Must not panic on a miss either.
	r.RecordFailure(unknown)
	r.RecordSuccess(unknown)
}

func TestRegistry_NewBreakersStartClosed(t *testing.T) {
	up := &config.Upstream{URL: "http://x"}
	r := NewRegistry([]*config.Upstream{up}, 1, time.Hour, testLogger())

	if !r.Allow(up) {
		t.Fatal("Allow() = false immediately after construction, want true: breakers must start closed, not open")
	}
}

func TestRegistry_TracksEachUpstreamIndependently(t *testing.T) {
	dead := &config.Upstream{URL: "http://dead"}
	alive := &config.Upstream{URL: "http://alive"}
	r := NewRegistry([]*config.Upstream{dead, alive}, 2, 10*time.Millisecond, testLogger())

	r.RecordFailure(dead)
	r.RecordFailure(dead)

	if r.Allow(dead) {
		t.Error("Allow(dead) = true, want false: should be open after 2 failures")
	}
	if !r.Allow(alive) {
		t.Error("Allow(alive) = false, want true: failures on one upstream must not affect another")
	}
}

func TestRegistry_RecoversAfterCooldown(t *testing.T) {
	up := &config.Upstream{URL: "http://x"}
	r := NewRegistry([]*config.Upstream{up}, 1, 10*time.Millisecond, testLogger())

	r.RecordFailure(up)
	if r.Allow(up) {
		t.Fatal("Allow() = true right after opening, want false")
	}

	time.Sleep(20 * time.Millisecond)
	if !r.Allow(up) {
		t.Fatal("Allow() = false after cooldown elapsed, want true (half-open trial)")
	}

	r.RecordSuccess(up)
	if !r.Allow(up) {
		t.Fatal("Allow() = false after a successful trial, want true: should be fully closed")
	}
}
