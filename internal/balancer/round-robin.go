package balancer

import (
	"sync/atomic"

	"github.com/PortableBaka/gateway/internal/config"
)

type RoundRobin struct {
	upstreams     []*config.Upstream
	healthChecker HealthChecker
	index         atomic.Uint64
}

func NewRoundRobin(upstreams []*config.Upstream, healthChecker HealthChecker) *RoundRobin {
	return &RoundRobin{
		upstreams:     upstreams,
		healthChecker: healthChecker,
		index:         atomic.Uint64{},
	}
}

func (r *RoundRobin) Next() *config.Upstream {
	n := len(r.upstreams)
	if n == 0 {
		return nil
	}

	// Bounded to n attempts: each iteration advances the shared counter and
	// tries the next upstream in rotation, so a run of unhealthy upstreams
	// gets skipped rather than retried, and if every upstream is unhealthy
	// this terminates instead of looping forever.
	for i := 0; i < n; i++ {
		index := r.index.Add(1) - 1
		upstream := r.upstreams[index%uint64(n)]

		if r.healthChecker == nil || r.healthChecker.IsHealthy(upstream) {
			return upstream
		}
	}

	return nil
}
