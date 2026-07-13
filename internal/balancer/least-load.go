package balancer

import (
	"sync/atomic"

	"github.com/PortableBaka/gateway/internal/config"
)

// Releaser is an optional interface a Balancer may additionally implement
// when picking an upstream needs a matching "this request finished" signal.
// LeastLoad tracks in-flight request counts, so every Next() that returns
// an upstream must eventually be paired with a Done() call for that same
// upstream, or its count only ever goes up. RoundRobin, Weighted, and
// Random don't implement this — none of them have state that depends on
// request completion, only on being picked, so there's nothing to release.
//
// This is deliberately not part of the core Balancer interface: forcing
// every strategy to implement a no-op Done would be interface pollution for
// the three that don't need it. Callers (proxy.go) type-assert for it
// instead — the same "optional interface" pattern the stdlib uses for
// http.Flusher, io.ReaderFrom, and friends.
type Releaser interface {
	Done(up *config.Upstream)
}

type leastLoadNode struct {
	upstream *config.Upstream
	inFlight atomic.Int64
}

// LeastLoad picks the healthy upstream with the fewest currently in-flight
// requests — approximating real server load by concurrency rather than a
// fixed rotation, which matters when upstreams have uneven per-request
// cost (round-robin would keep sending an even share to a backend that's
// slow to finish each one).
type LeastLoad struct {
	nodes         []*leastLoadNode
	healthChecker HealthChecker
}

func NewLeastLoad(upstreams []*config.Upstream, healthChecker HealthChecker) *LeastLoad {
	nodes := make([]*leastLoadNode, len(upstreams))
	for i, up := range upstreams {
		nodes[i] = &leastLoadNode{upstream: up}
	}
	return &LeastLoad{nodes: nodes, healthChecker: healthChecker}
}

func (l *LeastLoad) isHealthy(up *config.Upstream) bool {
	return l.healthChecker == nil || l.healthChecker.IsHealthy(up)
}

// Next scans every healthy node for the lowest current in-flight count and
// increments the winner's count as part of the pick — an "acquire", whose
// matching "release" is Done.
//
// The scan-then-increment isn't one atomic step, so two concurrent Next()
// calls can both read the same node as the current minimum and both pick it
// before either increment becomes visible to the other. That's a deliberate
// tradeoff, not an oversight: real least-connections balancers (nginx,
// HAProxy) accept the same benign race rather than serializing every pick
// behind one lock, which would throttle throughput under exactly the
// high-concurrency conditions this strategy exists for. Worst case is a
// brief, self-correcting double-up, not corrupted state — every individual
// counter operation is still atomic, so this is still safe for concurrent
// use per the Balancer contract, just not perfectly balanced under
// contention.
func (l *LeastLoad) Next() *config.Upstream {
	var best *leastLoadNode

	for _, n := range l.nodes {
		if !l.isHealthy(n.upstream) {
			continue
		}
		if best == nil || n.inFlight.Load() < best.inFlight.Load() {
			best = n
		}
	}

	if best == nil {
		return nil
	}

	best.inFlight.Add(1)

	return best.upstream
}

// Done releases the in-flight slot Next() acquired for up. Safe to call
// with an upstream Next() never actually returned (e.g. stale state) — it
// just won't find a matching node and does nothing.
func (l *LeastLoad) Done(up *config.Upstream) {
	for _, n := range l.nodes {
		if n.upstream == up {
			n.inFlight.Add(-1)
			return
		}
	}
}
