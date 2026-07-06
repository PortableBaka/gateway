package balancer

import (
	"sync/atomic"

	"github.com/PortableBaka/gateway/internal/config"
)

type RoundRobin struct {
	upstreams []*config.Upstream
	index     atomic.Uint64
}

func NewRoundRobin(upstreams []*config.Upstream) *RoundRobin {
	return &RoundRobin{
		upstreams: upstreams,
		index:     atomic.Uint64{},
	}
}

func (r *RoundRobin) Next() *config.Upstream {
	n := len(r.upstreams)
	if n == 0 {
		return nil
	}

	index := r.index.Add(1) - 1

	return r.upstreams[index%uint64(n)]
}
