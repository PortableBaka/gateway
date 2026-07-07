package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/PortableBaka/gateway/internal/config"
	"github.com/PortableBaka/gateway/internal/gateway"
	"github.com/PortableBaka/gateway/internal/middleware"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Build the gateway router (one proxy handler per route) and collect each
	// route's health.Checker so its background probing goroutines can be
	// started once we have a shutdown-aware context, below.
	router, checkers, err := gateway.New(cfg, logger)
	if err != nil {
		logger.Error("failed to build gateway", "error", err)
		os.Exit(1)
	}

	// Top-level mux: /healthz is handled directly, everything else falls
	// through to the gateway router (nested muxes — the more specific
	// "/healthz" pattern wins over the catch-all "/").
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.Handle("/", router)

	// Chain wraps every request — including /healthz — so the whole server
	// gets one consistent request-ID and one log line regardless of which
	// handler is hit. RequestIDMiddleware runs first so the ID it stashes in
	// the request context is there by the time LogMiddleware reads it back.
	handler := middleware.Chain(mux, middleware.RequestIDMiddleware, middleware.RecoverMiddleware(logger), middleware.LogMiddleware(logger))

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

	logger.Info("server starting", "addr", cfg.Server.Addr)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
