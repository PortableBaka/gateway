package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	yaml "gopkg.in/yaml.v3"
)

var (
	ErrNoRoutes = errors.New("no routes registered")
)

// validStrategies is the set of load-balancing strategies we support. Using a
// map as a set gives O(1) lookup and one obvious place to add new strategies.
var validStrategies = map[string]bool{
	RoundRobin: true,
	LeastLoad:  true,
	Random:     true,
	Weighted:   true,
}

const (
	RoundRobin = "round_robin"
	LeastLoad  = "least_load"
	Random     = "random"
	Weighted   = "weighted"
)

const (
	DefaultAddr            = ":8080"
	DefaultReadTimeout     = 10 * time.Second
	DefaultRouteTimeout    = 10 * time.Second
	DefaultWriteTimeout    = 10 * time.Second
	DefaultShutdownTimeout = 15 * time.Second

	DefaultHealthCheckPath     = "/"
	DefaultHealthCheckInterval = 5 * time.Second
	DefaultHealthCheckTimeout  = 2 * time.Second
)

const (
	DefaultHealthyThreshold   int32 = 2
	DefaultUnhealthyThreshold int32 = 2
)

const (
	DefaultBreakerCooldown  time.Duration = 10 * time.Second
	DefaultBreakerThreshold int32         = 5
)

const (
	DefaultRetryMaxAttempts int           = 3
	DefaultRetryBaseBackoff time.Duration = 50 * time.Millisecond
)

type Config struct {
	Server Server  `yaml:"server"`
	Routes []Route `yaml:"routes"`
}
type Server struct {
	Addr            string        `yaml:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}
type Route struct {
	PathPrefix  string        `yaml:"path_prefix"`
	Strategy    string        `yaml:"strategy"`
	Timeout     time.Duration `yaml:"timeout"`
	Upstreams   []Upstream    `yaml:"upstreams"`
	HealthCheck HealthCheck   `yaml:"health_check"`
	Breaker     Breaker       `yaml:"breaker"`
	Retry       Retry         `yaml:"retry"`
}
type Upstream struct {
	URL    string `yaml:"url"`
	Weight int    `yaml:"weight"`
}
type HealthCheck struct {
	Path               string        `yaml:"path"`
	Interval           time.Duration `yaml:"interval"`
	Timeout            time.Duration `yaml:"timeout"`
	HealthyThreshold   int32         `yaml:"healthy_threshold"`
	UnhealthyThreshold int32         `yaml:"unhealthy_threshold"`
}
type Breaker struct {
	FailureThreshold int           `yaml:"failure_threshold"`
	Cooldown         time.Duration `yaml:"cooldown"`
}
type Retry struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BaseBackoff time.Duration `yaml:"base_backoff"`
}

func LoadConfig(path string) (*Config, error) {
	var cfg Config

	file, err := os.Open(path)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}

	if cfg.Server.Addr == "" {
		cfg.Server.Addr = DefaultAddr
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = DefaultReadTimeout
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = DefaultWriteTimeout
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = DefaultShutdownTimeout
	}
	if len(cfg.Routes) == 0 {
		return nil, ErrNoRoutes
	}

	// Validate each route. We use the index in error messages so a bad config
	// tells the operator exactly where to look ("route 1 ..."), and wrap the
	// underlying error with %w so callers can still errors.Is/As it if needed.
	for i := range cfg.Routes {
		route := &cfg.Routes[i] // pointer so default assignments below persist

		if route.PathPrefix == "" {
			return nil, fmt.Errorf("route %d: path_prefix is required", i)
		}

		// Default + validate the strategy.
		if route.Strategy == "" {
			route.Strategy = RoundRobin
		}
		if route.Timeout <= 0 {
			route.Timeout = DefaultRouteTimeout
		}

		// Default the health-check block the same way: zero value means "not
		// set in YAML" for every field here, so there's no way to distinguish
		// an explicit zero from an absent one — treat both as "use the default".
		if route.HealthCheck.Path == "" {
			route.HealthCheck.Path = DefaultHealthCheckPath
		}
		if route.HealthCheck.Interval <= 0 {
			route.HealthCheck.Interval = DefaultHealthCheckInterval
		}
		if route.HealthCheck.Timeout <= 0 {
			route.HealthCheck.Timeout = DefaultHealthCheckTimeout
		}
		if route.HealthCheck.HealthyThreshold <= 0 {
			route.HealthCheck.HealthyThreshold = DefaultHealthyThreshold
		}
		if route.HealthCheck.UnhealthyThreshold <= 0 {
			route.HealthCheck.UnhealthyThreshold = DefaultUnhealthyThreshold
		}

		if route.Breaker.Cooldown <= 0 {
			route.Breaker.Cooldown = DefaultBreakerCooldown
		}

		if route.Breaker.FailureThreshold <= 0 {
			route.Breaker.FailureThreshold = int(DefaultBreakerThreshold)
		}

		if route.Retry.MaxAttempts <= 0 {
			route.Retry.MaxAttempts = DefaultRetryMaxAttempts
		}
		if route.Retry.BaseBackoff <= 0 {
			route.Retry.BaseBackoff = DefaultRetryBaseBackoff
		}

		if !validStrategies[route.Strategy] {
			return nil, fmt.Errorf("route %d (%s): unknown strategy %q", i, route.PathPrefix, route.Strategy)
		}

		if len(route.Upstreams) == 0 {
			return nil, fmt.Errorf("route %d (%s): has no upstreams", i, route.PathPrefix)
		}

		for j := range route.Upstreams {
			up := &route.Upstreams[j]

			// A zero weight would make the upstream invisible to weighted LB.
			if up.Weight <= 0 {
				up.Weight = 1
			}

			// Parse the URL and require an absolute address (scheme + host),
			// e.g. "http://localhost:9001". url.Parse is lenient and accepts
			// relative paths, so we check Scheme and Host explicitly.
			u, err := url.Parse(up.URL)
			if err != nil {
				return nil, fmt.Errorf("route %d (%s) upstream %d: invalid url %q: %w", i, route.PathPrefix, j, up.URL, err)
			}
			if u.Scheme == "" || u.Host == "" {
				return nil, fmt.Errorf("route %d (%s) upstream %d: url %q must include scheme and host", i, route.PathPrefix, j, up.URL)
			}
		}
	}

	return &cfg, nil
}
