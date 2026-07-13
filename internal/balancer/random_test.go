package balancer

import (
	"testing"

	"github.com/PortableBaka/gateway/internal/config"
)

// TestRandomDistribution doesn't pin an exact sequence (it's random by
// design) — it just checks that over enough picks, every upstream gets
// chosen at least once, which would fail fast if Next() were somehow always
// returning the same index.
func TestRandomDistribution(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	r := NewRandom(ups, nil)

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		counts[r.Next().URL]++
	}

	for _, u := range ups {
		if counts[u.URL] == 0 {
			t.Errorf("upstream %s was never picked in 300 draws", u.URL)
		}
	}
}

// fakeHealthChecker lets tests mark specific upstreams unhealthy without
// depending on the real health package.
type fakeHealthChecker struct {
	unhealthy map[*config.Upstream]bool
}

func (f fakeHealthChecker) IsHealthy(up *config.Upstream) bool {
	return !f.unhealthy[up]
}

func TestRandom_SkipsUnhealthyUpstreams(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	hc := fakeHealthChecker{unhealthy: map[*config.Upstream]bool{ups[0]: true, ups[1]: true}}
	r := NewRandom(ups, hc)

	for i := 0; i < 50; i++ {
		if got := r.Next(); got.URL != "c" {
			t.Fatalf("call %d: got %s, want the only healthy upstream (c)", i, got.URL)
		}
	}
}

func TestRandom_AllUnhealthyReturnsNil(t *testing.T) {
	ups := mkUpstreams("a", "b")
	hc := fakeHealthChecker{unhealthy: map[*config.Upstream]bool{ups[0]: true, ups[1]: true}}
	r := NewRandom(ups, hc)

	if got := r.Next(); got != nil {
		t.Errorf("got %v, want nil when every upstream is unhealthy", got)
	}
}
