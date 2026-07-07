package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/PortableBaka/gateway/internal/balancer"
	"github.com/PortableBaka/gateway/internal/config"
)

// NewRouteHandler builds an http.Handler that load-balances requests for a
// single route across its upstreams using a reverse proxy.
func NewRouteHandler(route *config.Route) (http.Handler, error) {
	// Build two things up front, once, so the per-request hot path stays cheap:
	//   1. ups     — stable *pointers* into the route's upstream slice, which is
	//                what the balancer hands back from Next().
	//   2. targets — each upstream's URL already parsed into a *url.URL, so we
	//                never re-parse the string on every request.
	ups := make([]*config.Upstream, len(route.Upstreams))
	targets := make(map[*config.Upstream]*url.URL, len(route.Upstreams))

	for i := range route.Upstreams {
		up := &route.Upstreams[i] // address of the slice element = stable pointer
		parsed, err := url.Parse(up.URL)
		if err != nil {
			// Config validation already checked this, but parse again defensively.
			return nil, err
		}
		ups[i] = up
		targets[up] = parsed
	}

	lb, err := balancer.NewBalancer(route.Strategy, ups)
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		// Rewrite runs once per incoming request. This is where we pick the
		// upstream (load balancing happens here) and point the outbound request
		// at it. pr.In is the request we received; pr.Out is the one being sent.
		Rewrite: func(pr *httputil.ProxyRequest) {
			up := lb.Next()

			if up == nil {
				// No upstream available. We can't write an error from Rewrite,
				// so we leave the request unrouted; ReverseProxy's ErrorHandler
				// will turn the resulting failure into a 502. Config validation
				// guarantees >=1 upstream, so this is just a safety net.
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
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)

		defer cancel()

		proxy.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}
