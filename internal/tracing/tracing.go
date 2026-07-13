// Package tracing wires up OpenTelemetry distributed tracing: a
// TracerProvider exporting spans either to stdout (for zero-infrastructure
// local verification) or to a real OTLP backend like Jaeger.
package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/PortableBaka/gateway/internal/config"
)

// Setup builds a TracerProvider from cfg, installs it as the global
// provider (so otelhttp's handler/transport wrapping — set up independently
// in main.go and proxy.go — picks it up automatically), and returns a
// shutdown func the caller must invoke during graceful shutdown to flush
// any spans still buffered in the batch processor. Only call this when
// cfg.Enabled is true.
func Setup(ctx context.Context, cfg config.Tracing, logger *slog.Logger) (func(context.Context) error, error) {
	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	// TraceContext (W3C traceparent header) is what otelhttp's handler and
	// transport wrapping use to propagate a trace across the gateway ->
	// upstream hop — without this, each hop would start its own disconnected
	// trace instead of one connected tree.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Info("tracing enabled", "endpoint", cfg.Endpoint, "service_name", cfg.ServiceName)

	return tp.Shutdown, nil
}

// newExporter picks stdout (endpoint == "stdout") for zero-infrastructure
// local verification — confirming spans are actually being created before
// bothering with a real backend — or OTLP-over-HTTP for anything else,
// e.g. a Jaeger instance's OTLP ingestion endpoint.
func newExporter(ctx context.Context, cfg config.Tracing) (sdktrace.SpanExporter, error) {
	if cfg.Endpoint == "stdout" {
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	return otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(cfg.Endpoint), otlptracehttp.WithInsecure())
}
