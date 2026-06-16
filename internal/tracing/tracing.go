// Package tracing wires OpenTelemetry distributed tracing for budgied. It is
// opt-in: Init is a no-op unless tracing is enabled, in which case it installs a
// global OTLP tracer provider and the W3C trace-context propagator so spans flow
// across nodes (e.g. through the write-region proxy). The HTTP transports are
// instrumented with otelhttp at the call sites; this package owns the provider
// lifecycle and configuration.
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config controls tracer setup.
type Config struct {
	Enabled     bool
	ServiceName string  // resource service.name (default "budgied")
	Version     string  // resource service.version
	NodeID      string  // budgie.node_id resource attribute
	SampleRatio float64 // 0..1 head sampling ratio (default 1.0)
}

// Shutdown flushes and tears down the tracer provider. Always non-nil.
type Shutdown func(context.Context) error

// Init installs the global tracer provider + propagator. When cfg.Enabled is
// false it returns a no-op Shutdown. The OTLP exporter reads its endpoint and
// headers from the standard OTEL_EXPORTER_OTLP_* environment variables.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	noop := func(context.Context) error { return nil }
	if !cfg.Enabled {
		return noop, nil
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "budgied"
	}
	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 1.0
	}

	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
	}
	if cfg.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.Version))
	}
	if cfg.NodeID != "" {
		attrs = append(attrs, attribute.String("budgie.node_id", cfg.NodeID))
	}
	res, err := resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, attrs...))
	if err != nil {
		// A schema-URL conflict shouldn't be fatal; fall back to just our attrs.
		res = resource.NewWithAttributes(semconv.SchemaURL, attrs...)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(tp)
	// Propagate W3C trace context + baggage so traces continue across nodes.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
