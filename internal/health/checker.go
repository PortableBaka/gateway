package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PortableBaka/gateway/internal/config"
)

// status is the per-upstream health state. healthy is seeded to true in
// NewChecker — atomic.Bool's zero value is false, which would otherwise mark
// every upstream unhealthy until its first check completes.
type status struct {
	healthy atomic.Bool
	fails   atomic.Int32
	oks     atomic.Int32
}

// recordFailure resets the success streak and, once consecutive failures
// reach threshold, flips healthy false. It reports whether this call caused
// an actual transition, so callers can log state changes once instead of on
// every single failed probe.
func (s *status) recordFailure(threshold int32) (changed bool) {
	s.oks.Store(0)
	if s.fails.Add(1) < threshold {
		return false
	}
	return s.healthy.CompareAndSwap(true, false)
}

func (s *status) recordSuccess(threshold int32) (changed bool) {
	s.fails.Store(0)
	if s.oks.Add(1) < threshold {
		return false
	}
	return s.healthy.CompareAndSwap(false, true)
}

// Checker tracks liveness for a fixed set of upstreams via active probing
// (Run) and accepts passive failure reports from the proxy (RecordFailure).
// The upstream set is fixed at construction time — only the atomic fields
// inside each *status mutate afterward — so the map itself needs no lock even
// though request-handling goroutines and health-check goroutines read/write
// it concurrently.
type Checker struct {
	upstreams          []*config.Upstream
	statuses           map[*config.Upstream]*status
	client             *http.Client
	path               string
	interval           time.Duration
	healthyThreshold   int32
	unhealthyThreshold int32
	logger             *slog.Logger
}

func NewChecker(upstreams []*config.Upstream, cfg config.HealthCheck, logger *slog.Logger) *Checker {
	statuses := make(map[*config.Upstream]*status, len(upstreams))
	for _, up := range upstreams {
		st := &status{}
		st.healthy.Store(true) // optimistic: assume healthy until proven otherwise
		statuses[up] = st
	}

	return &Checker{
		upstreams:          upstreams,
		statuses:           statuses,
		client:             &http.Client{Timeout: cfg.Timeout},
		path:               cfg.Path,
		interval:           cfg.Interval,
		healthyThreshold:   cfg.HealthyThreshold,
		unhealthyThreshold: cfg.UnhealthyThreshold,
		logger:             logger,
	}
}

// IsHealthy reports whether up should currently receive traffic. An upstream
// this Checker has no record of fails open (returns true) rather than
// silently dropping its traffic — in practice this only happens if it's ever
// called with an upstream from outside the list it was constructed with.
func (c *Checker) IsHealthy(up *config.Upstream) bool {
	st, ok := c.statuses[up]
	if !ok {
		return true
	}
	return st.healthy.Load()
}

func (c *Checker) RecordSuccess(up *config.Upstream) {
	st, ok := c.statuses[up]
	if !ok {
		return
	}
	if st.recordSuccess(c.healthyThreshold) {
		c.logger.Info("upstream marked healthy", "upstream", up.URL)
	}
}

func (c *Checker) RecordFailure(up *config.Upstream) {
	st, ok := c.statuses[up]
	if !ok {
		return
	}
	if st.recordFailure(c.unhealthyThreshold) {
		c.logger.Warn("upstream marked unhealthy", "upstream", up.URL)
	}
}

// Run starts one active-probing goroutine per upstream and blocks until ctx
// is cancelled and all of them have exited. Callers should invoke it as
// `go checker.Run(ctx)`; a per-upstream goroutine (rather than one loop over
// all upstreams) means a slow probe against one upstream never delays
// checking the others.
func (c *Checker) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for _, up := range c.upstreams {
		wg.Add(1)

		go func(up *config.Upstream) {
			defer wg.Done()

			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c.probe(up)
				}
			}
		}(up)
	}

	wg.Wait()
}

func (c *Checker) probe(up *config.Upstream) {
	req, err := http.NewRequest(http.MethodGet, up.URL+c.path, nil)
	if err != nil {
		c.RecordFailure(up)
		return
	}

	// c.client carries its own Timeout, so this bounds the whole
	// request/response cycle without needing a per-call context.
	resp, err := c.client.Do(req)
	if err != nil {
		c.RecordFailure(up)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // drain so the connection can be reused

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.RecordSuccess(up)
	} else {
		c.RecordFailure(up)
	}
}
