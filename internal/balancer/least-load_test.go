package balancer

import (
	"testing"

	"github.com/PortableBaka/gateway/internal/config"
)

func TestLeastLoad_PicksTheLeastLoadedUpstream(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	l := NewLeastLoad(ups, nil)

	// Load up two of the three upstreams so the third is the only one with
	// zero in-flight requests; Next() should always prefer it.
	first := l.Next()  // picks whichever node it scans first (all tied at 0)
	second := l.Next() // must differ from first, since first is now at 1
	if first.URL == second.URL {
		t.Fatalf("expected two different upstreams to be picked once each is loaded, got %s twice", first.URL)
	}

	// Now both "first" and "second" have 1 in-flight request each; the
	// third pick must go to whichever upstream is still at 0.
	third := l.Next()
	if third.URL == first.URL || third.URL == second.URL {
		t.Errorf("third pick = %s, want the still-idle third upstream", third.URL)
	}
}

func TestLeastLoad_DoneReleasesTheSlot(t *testing.T) {
	ups := mkUpstreams("a", "b")
	l := NewLeastLoad(ups, nil)

	first := l.Next() // e.g. a: 1 in-flight
	_ = l.Next()      // b: 1 in-flight
	l.Done(first)     // a: back to 0

	// With both upstreams having been picked once, but "first" released,
	// the next pick must go back to "first", not stay pinned on the other.
	if got := l.Next(); got.URL != first.URL {
		t.Errorf("Next() after Done = %s, want %s (its slot was released)", got.URL, first.URL)
	}
}

func TestLeastLoad_DoneOnUnknownUpstreamDoesNotPanic(t *testing.T) {
	ups := mkUpstreams("a")
	l := NewLeastLoad(ups, nil)

	unknown := &config.Upstream{URL: "not-tracked-by-this-balancer"}
	l.Done(unknown) // must be a no-op, not a panic
}

func TestLeastLoad_SkipsUnhealthyUpstreams(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	hc := fakeHealthChecker{unhealthy: map[*config.Upstream]bool{ups[0]: true, ups[1]: true}}
	l := NewLeastLoad(ups, hc)

	for i := 0; i < 5; i++ {
		if got := l.Next(); got.URL != "c" {
			t.Fatalf("call %d: got %s, want the only healthy upstream (c)", i, got.URL)
		}
	}
}

func TestLeastLoad_AllUnhealthyReturnsNil(t *testing.T) {
	ups := mkUpstreams("a", "b")
	hc := fakeHealthChecker{unhealthy: map[*config.Upstream]bool{ups[0]: true, ups[1]: true}}
	l := NewLeastLoad(ups, hc)

	if got := l.Next(); got != nil {
		t.Errorf("got %v, want nil when every upstream is unhealthy", got)
	}
}
