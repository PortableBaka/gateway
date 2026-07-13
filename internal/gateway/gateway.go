package gateway

import (
	"log/slog"
	"net/http"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/health"
	"github.com/PortableBaka/gateway/internal/metrics"
	"github.com/PortableBaka/gateway/internal/middleware"
	"github.com/PortableBaka/gateway/internal/proxy"
)

// New builds the gateway's HTTP router from config: one proxy handler per
// route, registered under its path prefix. It also returns each route's
// health.Checker so the caller can start (and stop, via context
// cancellation) their background probing goroutines.
func New(cfg *config.Config, logger *slog.Logger, m *metrics.Metrics) (http.Handler, []*health.Checker, error) {
	mux := http.NewServeMux()
	checkers := make([]*health.Checker, 0, len(cfg.Routes))

	// Built once, shared by every route: authMw is a pure function of the
	// gateway-wide key list, not anything route-specific.
	authMw := middleware.AuthMiddleware(cfg.Server.APIKeys, logger)

	for i := range cfg.Routes {
		route := &cfg.Routes[i] // pointer: the proxy holds references into this

		handler, checker, err := proxy.NewRouteHandler(route, logger, m, authMw)
		if err != nil {
			return nil, nil, err
		}

		// A ServeMux pattern ending in "/" is a subtree match: "/users/" matches
		// /users/ and everything beneath it. We also register the bare prefix so
		// a request to exactly "/users" (no trailing slash) still routes.
		mux.Handle(route.PathPrefix+"/", handler)
		mux.Handle(route.PathPrefix, handler)

		checkers = append(checkers, checker)
	}

	return mux, checkers, nil
}
