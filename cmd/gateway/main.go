package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* onto http.DefaultServeMux, served only by the separate debug server below
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/gateway"
	"github.com/PortableBaka/gateway/internal/health"
	"github.com/PortableBaka/gateway/internal/metrics"
	"github.com/PortableBaka/gateway/internal/middleware"
	"github.com/PortableBaka/gateway/internal/tracing"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// reloadableHandler lets the server swap its active handler atomically: a
// request already inside ServeHTTP has already Load()ed a specific handler
// value and keeps running against it regardless of any later Store, while
// requests arriving after a Store immediately see the new one. That's what
// makes a config reload not drop in-flight requests, with no explicit
// draining logic required.
type reloadableHandler struct {
	current atomic.Pointer[http.Handler]
}

func (h *reloadableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*h.current.Load()).ServeHTTP(w, r)
}

func (h *reloadableHandler) Store(next http.Handler) {
	h.current.Store(&next)
}

// buildHandler builds the full request-handling chain from cfg: the gateway
// router plus /healthz and /metrics, wrapped in the standard middleware
// chain. Used both at startup and on every config reload so the two paths
// can never drift apart.
func buildHandler(cfg *config.Config, logger *slog.Logger, m *metrics.Metrics, rateLimiter *middleware.RateLimiter) (http.Handler, []*health.Checker, error) {
	router, checkers, err := gateway.New(cfg, logger, m)
	if err != nil {
		return nil, nil, err
	}

	// Top-level mux: /healthz is handled directly, everything else falls
	// through to the gateway router (nested muxes — the more specific
	// "/healthz" pattern wins over the catch-all "/").
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	// promhttp.HandlerFor (not the simpler promhttp.Handler) because m uses
	// its own *prometheus.Registry instead of the global default one.
	mux.Handle("/metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
	mux.Handle("/", router)

	// Chain wraps every request — including /healthz — so the whole server
	// gets one consistent request-ID and one log line regardless of which
	// handler is hit. RequestIDMiddleware runs first so the ID it stashes in
	// the request context is there by the time LogMiddleware reads it back.
	// RateLimitMiddleware is innermost (right before mux) but still inside
	// LogMiddleware, so a rejected (429) request still gets logged exactly
	// like any other.
	handler := middleware.Chain(mux, middleware.RequestIDMiddleware, middleware.RecoverMiddleware(logger), middleware.LogMiddleware(logger), middleware.RateLimitMiddleware(rateLimiter, logger))

	return handler, checkers, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	configPath := "config.yaml"
	if v := os.Getenv("GATEWAY_CONFIG_PATH"); v != "" {
		configPath = v
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	m := metrics.New()

	var tracerShutdown func(context.Context) error
	if cfg.Server.Tracing.Enabled {
		shutdown, err := tracing.Setup(context.Background(), cfg.Server.Tracing, logger)
		if err != nil {
			logger.Error("failed to set up tracing", "error", err)
			os.Exit(1)
		}
		tracerShutdown = shutdown
	}

	// Built once, outside buildHandler: rate limits are per-client state
	// that must survive a config reload, not be reset by one.
	rateLimiter := middleware.NewRateLimiter(cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst)

	handler, checkers, err := buildHandler(cfg, logger, m, rateLimiter)
	if err != nil {
		logger.Error("failed to build gateway", "error", err)
		os.Exit(1)
	}

	rh := &reloadableHandler{}
	rh.Store(handler)

	// otelhttp.NewHandler wraps the reloadable handler, not the inner one
	// directly — span creation for the whole request lifecycle shouldn't
	// need rebuilding on every config reload, only the routing beneath it
	// does.
	var topHandler http.Handler = rh
	if cfg.Server.Tracing.Enabled {
		topHandler = otelhttp.NewHandler(rh, "gateway")
	}

	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      topHandler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// Health-check goroutines share the server's shutdown context, so they
	// exit on their own once ctx is cancelled below — no separate stop signal
	// needed for them.
	for _, checker := range checkers {
		go checker.Run(ctx)
	}
	go rateLimiter.Run(ctx, cfg.Server.RateLimit.CleanupInterval, cfg.Server.RateLimit.MaxIdle)

	// SIGHUP triggers a config reload, not shutdown, so it needs its own
	// signal channel — folding it into the ctx above would trigger shutdown
	// instead of a reload.
	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-reloadCh:
				newCfg, err := config.LoadConfig(configPath)
				if err != nil {
					logger.Error("reload failed: invalid config, keeping current", "error", err)
					continue
				}

				newHandler, newCheckers, err := buildHandler(newCfg, logger, m, rateLimiter)
				if err != nil {
					logger.Error("reload failed: could not build gateway", "error", err)
					continue
				}

				rh.Store(newHandler)

				// The previous generation's checkers are intentionally left
				// running until process shutdown rather than stopped here —
				// a known, bounded simplification (they just keep probing
				// upstreams that may no longer be routed to) rather than
				// building per-generation cancellable sub-contexts.
				for _, c := range newCheckers {
					go c.Run(ctx)
				}

				logger.Info("config reloaded")
			}
		}
	}()

	logger.Info("server starting", "addr", cfg.Server.Addr)

	var debugServer *http.Server
	if cfg.Server.DebugAddr != "" {
		debugServer = &http.Server{Addr: cfg.Server.DebugAddr}
		go func() {
			if err := debugServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("debug server error", "error", err)
			}
		}()
		logger.Info("debug server starting", "addr", cfg.Server.DebugAddr)
	}

	go func() {
		var err error
		if cfg.Server.TLS.CertFile != "" {
			httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}

			err = httpServer.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	defer stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)

	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}

	if debugServer != nil {
		if err := debugServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("debug server graceful shutdown failed", "error", err)
		}
	}

	if tracerShutdown != nil {
		if err := tracerShutdown(shutdownCtx); err != nil {
			logger.Error("tracer shutdown failed", "error", err)
		}
	}

	logger.Info("server stopped", "addr", cfg.Server.Addr)
}
