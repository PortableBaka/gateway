package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/PortableBaka/gateway/internal/balancer"
	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/health"
)

// NewRouteHandler builds an http.Handler that load-balances requests for a
// single route across its upstreams using a reverse proxy, backed by active
// and passive health checking. It returns the route's health.Checker
// alongside the handler so the caller can start (and, by cancelling its ctx,
// stop) its background probing goroutines.
func NewRouteHandler(route *config.Route, logger *slog.Logger) (http.Handler, *health.Checker, error) {
	// Build these up front, once, so the per-request hot path stays cheap:
	//   1. ups            — stable *pointers* into the route's upstream slice,
	//                       which is what the balancer hands back from Next().
	//   2. targets         — each upstream's URL already parsed into a *url.URL,
	//                       so we never re-parse the string on every request.
	//   3. hostToUpstream — the reverse of targets, keyed by host. ErrorHandler
	//                       below only sees the outbound *http.Request (already
	//                       rewritten to point at a target), not the
	//                       *config.Upstream we picked — this is how it maps
	//                       back to report a failure against the right one.
	ups := make([]*config.Upstream, len(route.Upstreams))
	targets := make(map[*config.Upstream]*url.URL, len(route.Upstreams))
	hostToUpstream := make(map[string]*config.Upstream, len(route.Upstreams))

	for i := range route.Upstreams {
		up := &route.Upstreams[i] // address of the slice element = stable pointer
		parsed, err := url.Parse(up.URL)
		if err != nil {
			// Config validation already checked this, but parse again defensively.
			return nil, nil, err
		}
		ups[i] = up
		targets[up] = parsed
		hostToUpstream[parsed.Host] = up
	}

	checker := health.NewChecker(ups, route.HealthCheck, logger)

	lb, err := balancer.NewBalancer(route.Strategy, ups, checker)
	if err != nil {
		return nil, nil, err
	}

	proxy := &httputil.ReverseProxy{
		// Rewrite runs once per incoming request. This is where we pick the
		// upstream (load balancing happens here) and point the outbound request
		// at it. pr.In is the request we received; pr.Out is the one being sent.
		Rewrite: func(pr *httputil.ProxyRequest) {
			up := lb.Next()

			if up == nil {
				// No upstream available (none configured, or all unhealthy).
				// We can't write an error from Rewrite, so we leave the request
				// unrouted; ErrorHandler below turns the resulting failure into
				// a 502.
				return
			}

			// SetURL sets the outbound scheme+host to the target and joins the
			// target's path with the incoming request path (so /users/42 on
			// http://localhost:9001 becomes http://localhost:9001/users/42).
			pr.SetURL(targets[up])

			// Add X-Forwarded-For / -Host / -Proto so the upstream can see the
			// original client details through the proxy.
			pr.SetXForwarded()
		},
		// ErrorHandler is the passive half of health checking. Active probes
		// only run every route.HealthCheck.Interval, so without this a dead
		// upstream keeps taking (and failing) live traffic until the next
		// tick; reporting the failure here, the instant it happens, closes
		// that gap. It replaces ReverseProxy's default ErrorHandler, so it
		// also has to do that handler's job: log and write the 502.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if up, ok := hostToUpstream[r.URL.Host]; ok {
				checker.RecordFailure(up)
			}

			// RequestIDMiddleware sets this response header before the proxy
			// ever runs, so it's already there to read back for correlation.
			logger.Error("proxy error", "error", err, "path", r.URL.Path, "request_id", w.Header().Get("X-Request-Id"))

			w.WriteHeader(http.StatusBadGateway)
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)

		defer cancel()

		proxy.ServeHTTP(w, r.WithContext(ctx))
	})

	return handler, checker, nil
}
