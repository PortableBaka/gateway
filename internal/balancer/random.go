package balancer

import (
	"math/rand/v2"

	"github.com/PortableBaka/gateway/internal/config"
)

// Random picks a uniformly random healthy upstream on every call. Unlike
// RoundRobin or Weighted, it needs no shared mutable state at all — no
// counter, no mutex — since a pick never depends on any previous pick.
// math/rand/v2's top-level functions are already safe for concurrent use,
// so there's nothing to guard here.
type Random struct {
	upstreams     []*config.Upstream
	healthChecker HealthChecker
}

func NewRandom(upstreams []*config.Upstream, healthChecker HealthChecker) *Random {
	return &Random{upstreams: upstreams, healthChecker: healthChecker}
}

func (r *Random) Next() *config.Upstream {
	if r.healthChecker == nil {
		if len(r.upstreams) == 0 {
			return nil
		}
		return r.upstreams[rand.IntN(len(r.upstreams))]
	}

	// Filter to healthy upstreams first, then draw once from that set,
	// rather than drawing blind and retrying up to n times. That bounded-
	// retry shape works for RoundRobin because its modular indexing
	// deterministically visits every distinct upstream within n attempts —
	// but random sampling with replacement doesn't have that guarantee: with
	// most upstreams unhealthy, a fixed number of blind draws can miss the
	// few healthy ones entirely (e.g. 2 of 3 unhealthy gives roughly a 30%
	// chance per call of drawing unhealthy three times running), returning
	// nil — and therefore a spurious 502 — even though a healthy upstream
	// exists. Filtering first makes finding one, when one exists,
	// guaranteed rather than probabilistic; the tradeoff is one slice
	// allocation per call, which every other strategy here avoids.
	healthy := make([]*config.Upstream, 0, len(r.upstreams))
	for _, up := range r.upstreams {
		if r.healthChecker.IsHealthy(up) {
			healthy = append(healthy, up)
		}
	}

	if len(healthy) == 0 {
		return nil
	}

	return healthy[rand.IntN(len(healthy))]
}
