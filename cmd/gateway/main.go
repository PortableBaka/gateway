package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/gateway"
	"github.com/PortableBaka/gateway/internal/metrics"
	"github.com/PortableBaka/gateway/internal/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
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

	// Build the gateway router (one proxy handler per route) and collect each
	// route's health.Checker so its background probing goroutines can be
	// started once we have a shutdown-aware context, below.
	router, checkers, err := gateway.New(cfg, logger, m)
	if err != nil {
		logger.Error("failed to build gateway", "error", err)
		os.Exit(1)
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

	rateLimiter := middleware.NewRateLimiter(cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst)

	// Chain wraps every request — including /healthz — so the whole server
	// gets one consistent request-ID and one log line regardless of which
	// handler is hit. RequestIDMiddleware runs first so the ID it stashes in
	// the request context is there by the time LogMiddleware reads it back.
	// RateLimitMiddleware is innermost (right before mux) but still inside
	// LogMiddleware, so a rejected (429) request still gets logged exactly
	// like any other.
	handler := middleware.Chain(mux, middleware.RequestIDMiddleware, middleware.RecoverMiddleware(logger), middleware.LogMiddleware(logger), middleware.RateLimitMiddleware(rateLimiter, logger))

	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
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

	logger.Info("server starting", "addr", cfg.Server.Addr)

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

	logger.Info("server stopped", "addr", cfg.Server.Addr)
}
