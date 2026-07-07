package main

import (
	"context"
	"errors"
	"log"
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
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Build the gateway router (one proxy handler per route).
	router, err := gateway.New(cfg)
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}

	// Top-level mux: /healthz is handled directly, everything else falls
	// through to the gateway router (nested muxes — the more specific
	// "/healthz" pattern wins over the catch-all "/").
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.Handle("/", router)

	// Chain wraps every request — including /healthz — so the whole server
	// gets one consistent request-ID regardless of which handler is hit.
	handler := middleware.Chain(mux, middleware.RequestIDMiddleware)

	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	log.Printf("Server starting on %s...", cfg.Server.Addr)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	defer stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)

	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}

	log.Printf("Server stopped on %s.", cfg.Server.Addr)
}
