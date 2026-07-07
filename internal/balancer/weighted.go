package balancer

import (
	"sync"

	"github.com/PortableBaka/gateway/internal/config"
)

// weightedNode wraps an upstream with the mutable running score used by the
// smooth weighted round-robin algorithm. We can't store this on config.Upstream
// because that's shared config, so the balancer keeps its own per-node state.
type weightedNode struct {
	upstream *config.Upstream
	weight   int // configured (effective) weight, never changes
	current  int // running score, mutated on every Next call
}

type Weighted struct {
	mu            sync.Mutex
	nodes         []weightedNode
	total         int
	healthChecker HealthChecker
}

func NewWeighted(upstreams []*config.Upstream, healthChecker HealthChecker) *Weighted {
	w := &Weighted{nodes: make([]weightedNode, len(upstreams)), healthChecker: healthChecker}
	for i, up := range upstreams {
		w.nodes[i] = weightedNode{upstream: up, weight: up.Weight}
		w.total += up.Weight
	}
	return w
}

func (w *Weighted) isHealthy(up *config.Upstream) bool {
	return w.healthChecker == nil || w.healthChecker.IsHealthy(up)
}

// Next implements smooth weighted round-robin (the nginx algorithm):
//  1. add each node's configured weight to its running "current" score,
//  2. pick the node with the highest current score,
//  3. subtract the total weight from the winner.
//
// Compared to the naive "repeat each upstream weight times" approach, this
// spreads the picks out evenly (for weights 1:2 you get b,a,b rather than
// b,b,a) instead of bursting one upstream's whole share consecutively.
//
// Unhealthy nodes are excluded entirely rather than just skipped as a pick:
// their `current` score is frozen (not incremented) while they're down, and
// the amount subtracted from the winner is the total weight of only the
// currently-healthy nodes. Both matter for the same reason — if we kept
// incrementing a down node's score or kept subtracting the full (unhealthy-
// inclusive) total, a node coming back healthy would either burst through a
// backlog of accumulated credit or get shortchanged relative to its
// configured share; freezing it keeps the ratio correct among whichever
// nodes are actually healthy at any given moment.
//
// A mutex (not atomics) is needed here because a pick reads and writes every
// node's score as one indivisible step.
func (w *Weighted) Next() *config.Upstream {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.nodes) == 0 {
		return nil
	}

	var best *weightedNode
	healthyTotal := 0

	for i := range w.nodes {
		n := &w.nodes[i]

		if !w.isHealthy(n.upstream) {
			continue
		}

		healthyTotal += n.weight
		n.current += n.weight

		if best == nil || n.current > best.current {
			best = n
		}
	}

	if best == nil {
		return nil
	}

	best.current -= healthyTotal

	return best.upstream
}
