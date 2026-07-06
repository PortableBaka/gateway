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
	mu    sync.Mutex
	nodes []weightedNode
	total int
}

func NewWeighted(upstreams []*config.Upstream) *Weighted {
	w := &Weighted{nodes: make([]weightedNode, len(upstreams))}
	for i, up := range upstreams {
		w.nodes[i] = weightedNode{upstream: up, weight: up.Weight}
		w.total += up.Weight
	}
	return w
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
// A mutex (not atomics) is needed here because a pick reads and writes every
// node's score as one indivisible step.
func (w *Weighted) Next() *config.Upstream {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.nodes) == 0 {
		return nil
	}

	var best *weightedNode
	for i := range w.nodes {
		n := &w.nodes[i]
		n.current += n.weight
		if best == nil || n.current > best.current {
			best = n
		}
	}

	best.current -= w.total

	return best.upstream
}
