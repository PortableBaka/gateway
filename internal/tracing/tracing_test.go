package tracing

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/PortableBaka/gateway/internal/config"
)

// TestSetup_StdoutExporterInstallsGlobalProviderAndShutsDownCleanly uses the
// "stdout" endpoint deliberately: it needs no real collector running, so
// this exercises the full Setup/Shutdown lifecycle without any network
// dependency, while still going through the exact same TracerProvider
// wiring the OTLP path uses.
func TestSetup_StdoutExporterInstallsGlobalProviderAndShutsDownCleanly(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	cfg := config.Tracing{Enabled: true, Endpoint: "stdout", ServiceName: "test-service"}

	shutdown, err := Setup(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown func")
	}

	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Fatal("otel.GetTracerProvider() returned nil after Setup")
	}

	// A real (non-no-op) tracer must actually start spans that report as
	// recording — confirms Setup installed a real SDK provider, not left
	// the global no-op provider in place.
	_, span := tp.Tracer("test").Start(context.Background(), "test-span")
	if !span.IsRecording() {
		t.Error("span.IsRecording() = false, want true: TracerProvider should be a real SDK provider after Setup")
	}
	span.End()

	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}
