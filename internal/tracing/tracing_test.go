package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestInitDisabledIsNoop(t *testing.T) {
	sd, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("disabled Init should not error: %v", err)
	}
	if sd == nil {
		t.Fatal("Init must always return a non-nil shutdown")
	}
	if err := sd(context.Background()); err != nil {
		t.Fatalf("no-op shutdown should not error: %v", err)
	}
}

func TestInitEnabledInstallsW3CPropagator(t *testing.T) {
	// The OTLP exporter is created lazily (no connection at init), so this works
	// without a running collector.
	sd, err := Init(context.Background(), Config{Enabled: true, ServiceName: "budgied-test", NodeID: "node-1"})
	if err != nil {
		t.Fatalf("enabled Init failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sd(ctx)
	}()

	// A fabricated, sampled span context should produce a W3C traceparent when
	// injected via the globally-installed propagator — proving cross-node
	// propagation is wired.
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Fatal("expected a W3C traceparent header to be injected after Init")
	}
}
