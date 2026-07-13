package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/PortableBaka/gateway/internal/balancer"
	"github.com/PortableBaka/gateway/internal/breaker"
	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/health"
	"github.com/PortableBaka/gateway/internal/metrics"
	"github.com/PortableBaka/gateway/internal/middleware"
)

type combinedHealth struct {
	checker  *health.Checker
	breakers *breaker.Registry
}

func (c combinedHealth) IsHealthy(up *config.Upstream) bool {
	return c.checker.IsHealthy(up) && c.breakers.Allow(up)
}

// errorResponse is the JSON body written for any request that never gets a
// real upstream response — either httputil.ReverseProxy's ErrorHandler fired
// (transport failure), or Rewrite couldn't route it at all (no healthy
// upstream), which also ends up in ErrorHandler by way of a transport error.
type errorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}

// isIdempotent reports whether method is safe to retry against a different
// upstream: no side effects, so sending it more than once can't corrupt
// anything. PUT/DELETE are technically idempotent too, but not "safe" —
// retrying them can still double up on logging/metrics/etc. — so they're
// deliberately excluded here rather than auto-retried.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// retryBackoff returns the wait before retry attempt N (1-indexed), doubling
// each time and capped so a high MaxAttempts can't make a single request
// wait unreasonably long between tries.
func retryBackoff(attempt int, base time.Duration) time.Duration {
	const maxBackoff = 500 * time.Millisecond

	d := base * time.Duration(1<<(attempt-1))
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// NewRouteHandler builds an http.Handler that load-balances requests for a
// single route across its upstreams using a reverse proxy, backed by active
// and passive health checking. It returns the route's health.Checker
// alongside the handler so the caller can start (and, by cancelling its ctx,
// stop) its background probing goroutines.
func NewRouteHandler(route *config.Route, logger *slog.Logger, m *metrics.Metrics) (http.Handler, *health.Checker, error) {
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

	checker := health.NewChecker(ups, route.HealthCheck, logger, route.PathPrefix, m)
	breakers := breaker.NewRegistry(ups, route.Breaker.FailureThreshold, route.Breaker.Cooldown, logger)
	combined := combinedHealth{
		checker:  checker,
		breakers: breakers,
	}

	// The balancer must see combined (health check AND breaker), not checker
	// alone — otherwise the breaker only ever records outcomes and never
	// actually keeps traffic away from an open-circuit upstream.
	lb, err := balancer.NewBalancer(route.Strategy, ups, combined)
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
				breakers.RecordFailure(up)
			}

			// Read the request ID from context, not the response header: w
			// here may be a per-attempt httptest.ResponseRecorder (see the
			// retry loop below), which never had RequestIDMiddleware's
			// header set on it — only the real, outer ResponseWriter did.
			// The request's context, unlike the header, survives unchanged
			// across every retry attempt.
			requestID := middleware.GetRequestId(r.Context())
			logger.Error("proxy error", "error", err, "path", r.URL.Path, "request_id", requestID)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(errorResponse{Error: "upstream_unavailable", RequestID: requestID})
		},
		ModifyResponse: func(r *http.Response) error {
			up, ok := hostToUpstream[r.Request.URL.Host]

			if !ok {
				return nil
			}
			if r.StatusCode >= 500 {
				breakers.RecordFailure(up)
			} else {
				breakers.RecordSuccess(up)
			}

			return nil
		},
	}

	// Defensive defaults, same reasoning as the URL re-parse above: LoadConfig
	// already defaults these, but a Route built directly (e.g. in a test)
	// bypasses that, and a zero MaxAttempts would make the loop below never
	// run at all.
	maxAttempts := route.Retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	baseBackoff := route.Retry.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = config.DefaultRetryBaseBackoff
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)
		defer cancel()
		r = r.WithContext(ctx)

		retryable := isIdempotent(r.Method)

		// proxy.ServeHTTP writes straight to whatever ResponseWriter it's
		// given. If we let it write to the real w and then decided to retry,
		// we couldn't take back headers/body already sent to the client — so
		// each attempt writes into its own buffer, and only the last one
		// (the one we stop retrying on) gets copied to the real w.
		rec := httptest.NewRecorder()

		requestStart := time.Now()

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			rec = httptest.NewRecorder()
			proxy.ServeHTTP(rec, r)

			if !retryable || rec.Code < 500 || attempt == maxAttempts {
				break
			}

			logger.Warn("retrying request", "attempt", attempt, "method", r.Method, "path", r.URL.Path, "status", rec.Code)

			select {
			case <-time.After(retryBackoff(attempt, baseBackoff)):
			case <-ctx.Done():
			}
		}

		// route.PathPrefix, not r.URL.Path: the raw path is unbounded
		// cardinality (/users/1, /users/2, ... would each become a distinct
		// Prometheus time series), while the configured prefix is bounded to
		// one value per route.
		m.ObserveRequest(route.PathPrefix, r.Method, rec.Code, time.Since(requestStart))

		for k, vv := range rec.Header() {
			w.Header()[k] = vv
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	})

	return handler, checker, nil
}
