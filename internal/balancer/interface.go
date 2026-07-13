package balancer

import (
	"fmt"

	"github.com/PortableBaka/gateway/internal/config"
)

// Balancer picks the next upstream to send a request to. Implementations must
// be safe for concurrent use: Next is called from many request goroutines.
type Balancer interface {
	Next() *config.Upstream
}

type HealthChecker interface {
	IsHealthy(up *config.Upstream) bool
}

// NewBalancer builds the Balancer for a given strategy. It returns an error
// (rather than exiting) so the caller decides how to handle a bad config.
func NewBalancer(strategy string, upstreams []*config.Upstream, healthChecker HealthChecker) (Balancer, error) {
	switch strategy {
	case config.RoundRobin:
		return NewRoundRobin(upstreams, healthChecker), nil
	case config.Weighted:
		return NewWeighted(upstreams, healthChecker), nil
	case config.LeastLoad:
		return NewLeastLoad(upstreams, healthChecker), nil
	case config.Random:
		return NewRandom(upstreams, healthChecker), nil
	}

	return nil, fmt.Errorf("balancer: unsupported strategy %q", strategy)
}
