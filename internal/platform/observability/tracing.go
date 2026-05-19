// Package observability owns the process-wide telemetry setup —
// currently just OpenTelemetry tracing (DSN-004). Metrics continue
// to flow through the existing Prometheus scrape on /metrics; OTLP
// metrics adoption will follow when the collector story is settled.
package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracingConfig holds the inputs InitTracing needs. ServiceName
// and ServiceVersion become resource attributes so traces are
// attributable in the collector. SamplingRatio is the root-span
// sample rate (0.0–1.0); child spans inherit their parent's
// sampling decision via parent-based sampling.
type TracingConfig struct {
	ServiceName    string
	ServiceVersion string
	SamplingRatio  float64
}

// ShutdownFunc tears down the tracer provider, flushing any
// pending spans within the supplied context's deadline. Always
// call this from main's deferred shutdown; otherwise spans that
// haven't been batched out yet are lost.
type ShutdownFunc func(ctx context.Context) error

// InitTracing wires up the OpenTelemetry tracer provider against
// an OTLP/gRPC exporter pointed at OTEL_EXPORTER_OTLP_ENDPOINT.
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is empty (the local-dev / test
// case) tracing is installed as a no-op. The application code
// still calls otel.Tracer(...).Start(...) freely; spans are just
// dropped instead of exported. This means dev environments don't
// need a collector running.
//
// Sampling is parent-based: child spans of a sampled parent are
// always sampled, child spans of an unsampled parent are always
// dropped. Root spans use a TraceIDRatioBased sampler with
// cfg.SamplingRatio (override via OTEL_TRACES_SAMPLER_ARG; the
// SDK reads that env var natively).
//
// Propagation is W3C TraceContext + Baggage — the standard
// composite that interoperates with every modern collector and
// downstream service.
func InitTracing(ctx context.Context, cfg TracingConfig) (ShutdownFunc, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		log.Info().Msg("OTEL_EXPORTER_OTLP_ENDPOINT unset; installing no-op tracer (DSN-004)")
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build OTel resource: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.SamplingRatio),
		)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Info().
		Str("endpoint", endpoint).
		Str("service.name", cfg.ServiceName).
		Float64("sampling_ratio", cfg.SamplingRatio).
		Msg("OpenTelemetry tracing initialised")

	return tp.Shutdown, nil
}

// ResolveSamplingRatio reads OTEL_TRACES_SAMPLER_ARG (the SDK's
// standard env var for ratio sampling). Out-of-range or
// unparseable values fall back to defaultRatio with a warning.
func ResolveSamplingRatio(defaultRatio float64) float64 {
	raw := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if raw == "" {
		return defaultRatio
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		log.Warn().
			Str("OTEL_TRACES_SAMPLER_ARG", raw).
			Float64("default", defaultRatio).
			Msg("invalid sampling ratio; using default")
		return defaultRatio
	}
	return v
}

// Tracer returns a named tracer from the global provider. Every
// caller in the project should funnel through this so a future
// migration off the global doesn't have to chase otel.Tracer
// imports.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// StartServiceSpan opens a span on the named tracer with the given
// span name and attributes and returns the new ctx plus a finisher
// closure. Callers deferred-invoke the closure with their named
// error return — non-nil error records the error on the span and
// flips its status to Error before End. This is the boilerplate
// every internal service method runs through for DSN-004b's
// business-operation tracing.
//
// Pattern:
//
//	func (s *service) Reserve(ctx context.Context, rr ReservationRequest) (res Reservation, err error) {
//	    ctx, end := observability.StartServiceSpan(ctx, "inventory.Service", "Reserve",
//	        attribute.String("sku", rr.Sku),
//	    )
//	    defer func() { end(err) }()
//	    // ... body ...
//	}
func StartServiceSpan(ctx context.Context, tracerName, spanName string, attrs ...attribute.KeyValue) (context.Context, func(error)) {
	ctx, span := Tracer(tracerName).Start(ctx, spanName, trace.WithAttributes(attrs...))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

// flushTimeout caps how long shutdown will wait for the batch
// exporter to drain. Kept short — the orchestrator's grace
// period is the actual ceiling.
const flushTimeout = 5 * time.Second

// Flush is a convenience for callers that want to force an
// in-flight span batch to the collector outside the normal
// shutdown path (rarely useful in production; handy in tests).
func Flush(ctx context.Context) error {
	tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		return nil
	}
	flushCtx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("flush tracer: %w", err)
	}
	return nil
}
