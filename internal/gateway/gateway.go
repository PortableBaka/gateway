package gateway

import (
	"net/http"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/proxy"
)

// New builds the gateway's HTTP router from config: one proxy handler per
// route, registered under its path prefix.
func New(cfg *config.Config) (http.Handler, error) {
	mux := http.NewServeMux()

	for i := range cfg.Routes {
		route := &cfg.Routes[i] // pointer: the proxy holds references into this

		handler, err := proxy.NewRouteHandler(route)
		if err != nil {
			return nil, err
		}

		// A ServeMux pattern ending in "/" is a subtree match: "/users/" matches
		// /users/ and everything beneath it. We also register the bare prefix so
		// a request to exactly "/users" (no trailing slash) still routes.
		mux.Handle(route.PathPrefix+"/", handler)
		mux.Handle(route.PathPrefix, handler)
	}

	return mux, nil
}
