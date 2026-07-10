package breaker

import (
	"log/slog"
	"time"

	"github.com/PortableBaka/gateway/internal/config"
)

type Registry struct {
	breakers map[*config.Upstream]*Breaker
	logger   *slog.Logger
}

func NewRegistry(upstreams []*config.Upstream, failureThreshold int, cooldown time.Duration, logger *slog.Logger) *Registry {
	breakers := make(map[*config.Upstream]*Breaker)

	for _, upstream := range upstreams {
		// state is left at its zero value (closed) deliberately: a breaker
		// should start trusting its upstream until proven otherwise, same as
		// health.Checker seeding healthy=true. Starting open would circuit-
		// break every upstream on every gateway restart until the first
		// cooldown elapses.
		breaker := Breaker{
			failuresThreshold: failureThreshold,
			cooldown:          cooldown,
		}

		breakers[upstream] = &breaker
	}

	return &Registry{
		breakers: breakers,
		logger:   logger,
	}
}

// Allow reports whether up should currently receive traffic. An upstream
// this Registry has no record of fails open (returns true) rather than
// panicking or silently dropping its traffic.
func (r *Registry) Allow(up *config.Upstream) bool {
	b, ok := r.breakers[up]
	if !ok {
		return true
	}
	return b.Allow()
}

func (r *Registry) RecordSuccess(up *config.Upstream) {
	b, ok := r.breakers[up]
	if !ok {
		return
	}
	if b.RecordSuccess() {
		r.logger.Info("circuit breaker closed", "upstream", up.URL)
	}
}

func (r *Registry) RecordFailure(up *config.Upstream) {
	b, ok := r.breakers[up]
	if !ok {
		return
	}
	if b.RecordFailure() {
		r.logger.Warn("circuit breaker opened", "upstream", up.URL)
	}
}
