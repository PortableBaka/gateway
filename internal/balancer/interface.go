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

// NewBalancer builds the Balancer for a given strategy. It returns an error
// (rather than exiting) so the caller decides how to handle a bad config.
func NewBalancer(strategy string, upstreams []*config.Upstream) (Balancer, error) {
	switch strategy {
	case config.RoundRobin:
		return NewRoundRobin(upstreams), nil
	case config.Weighted:
		return NewWeighted(upstreams), nil
	case config.LeastLoad:
		TODO("least_load balancer strategy")
	case config.Random:
		TODO("random balancer strategy")
	}

	return nil, fmt.Errorf("balancer: unsupported strategy %q", strategy)
}

// TODO marks a code path as deliberately unimplemented, the same role Rust's
// todo!() macro plays: config.validStrategies already lists least_load and
// random as accepted values, so this documents the intent to support them
// without pretending they work. It panics rather than returning an error
// because reaching it means a route slipped through config validation with a
// strategy nothing implements yet — a startup bug, not a runtime condition
// callers should handle.
func TODO(feature string) {
	panic(fmt.Sprintf("balancer: %s is not implemented yet (TODO)", feature))
}
