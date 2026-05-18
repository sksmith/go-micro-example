package observability_test

import (
	"context"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInitTracingWithoutEndpointInstallsNoOp(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	shutdown, err := observability.InitTracing(context.Background(), observability.TracingConfig{
		ServiceName:    "test",
		ServiceVersion: "0.0.0",
		SamplingRatio:  1.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	// The global provider should now be the noop.TracerProvider.
	// Comparing types via fmt.Sprintf("%T") avoids reflect.DeepEqual's
	// noise on internal fields.
	tp := otel.GetTracerProvider()
	if _, ok := tp.(noop.TracerProvider); !ok {
		t.Errorf("expected noop.TracerProvider, got %T", tp)
	}

	// Calling Tracer().Start on the noop provider must not panic
	// and must return a usable (non-recording) span.
	_, span := observability.Tracer("unit-test").Start(context.Background(), "noop-span")
	span.End()
}

func TestResolveSamplingRatio(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want float64
	}{
		{name: "unset uses default", env: "", want: 0.5},
		{name: "valid mid-range", env: "0.25", want: 0.25},
		{name: "exactly 1.0", env: "1.0", want: 1.0},
		{name: "exactly 0.0", env: "0", want: 0},
		{name: "out-of-range high falls back", env: "1.5", want: 0.5},
		{name: "out-of-range negative falls back", env: "-0.1", want: 0.5},
		{name: "non-numeric falls back", env: "yes please", want: 0.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", test.env)
			if got := observability.ResolveSamplingRatio(0.5); got != test.want {
				t.Errorf("got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestFlushOnNoopIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	shutdown, err := observability.InitTracing(context.Background(), observability.TracingConfig{
		ServiceName:    "test",
		ServiceVersion: "0.0.0",
		SamplingRatio:  1.0,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	// Flush against a noop provider should return nil immediately,
	// never block.
	if err := observability.Flush(context.Background()); err != nil {
		t.Errorf("flush on noop: %v", err)
	}
}
