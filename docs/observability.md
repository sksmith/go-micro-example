# Observability

This service emits two complementary telemetry streams:

| Stream | Transport | Where it shows up |
| ------------- | ---------------------------- | ----------------------------------------------------- |
| **Metrics** | Prometheus scrape | `GET /metrics` (Prometheus text format) |
| **Traces** | OpenTelemetry OTLP/gRPC | Whatever collector `OTEL_EXPORTER_OTLP_ENDPOINT` points at |

OTLP metrics adoption is deliberately deferred — the existing
Prometheus surface is stable and the collector story for OTLP
metrics still varies by vendor. Traces are where this section
focuses.

## Tracing (DSN-004)

### What's instrumented

- **HTTP server** — every request wrapped in a span by
  [`otelchi`](https://github.com/riandyrn/otelchi). Span name is
  the chi route pattern (e.g. `GET /api/v1/inventory/{sku}`),
  not the literal URL with the parameter interpolated, so traces
  group cleanly per endpoint.
- **Database** — every query wrapped by
  [`otelpgx`](https://github.com/exaring/otelpgx), composed
  alongside the existing zerolog tracelog via
  `pgx/v5/multitracer`. Spans carry the SQL command and the
  query text (without bound parameters).
- **Anything called with `r.Context()`** — through W3C
  TraceContext propagation, so future outbound HTTP calls or
  gRPC clients pick up the active span automatically.

### Service-layer spans (DSN-004b)

Every exported method on `inventory.Service` and `user.Service`
opens an `INTERNAL`-kind span named after the method
(`inventory.Service.Reserve`, `user.Service.Login`, etc.) with the
relevant business identifiers as attributes: `inventory.sku`,
`request_id`, `inventory.reservation_id`, `user.username`. Errors
returned by the method are recorded via `span.RecordError` and the
span status flips to `Error`. The plumbing lives in
`observability.StartServiceSpan`, a helper that returns a finisher
closure callers `defer` with their named-error return.

These spans sit between otelchi's HTTP root and otelpgx's per-query
spans, so traces of a fanout request (one HTTP call → many DB
calls) make it obvious which DB spans came from which service
operation.

### Configuration

Three env vars drive the tracing setup; all are read by the OTel
SDK natively (no custom parsing):

| Variable | Default | What it does |
| ----------------------------------- | --------- | -------------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | If unset, tracing installs a no-op provider — call sites still `tracer.Start()` freely, the spans go nowhere. **No collector is required for local dev.** When set, OTLP/gRPC connects to the given endpoint (e.g. `http://otel-collector:4317`). |
| `OTEL_RESOURCE_ATTRIBUTES` | unset | Comma-separated `k=v` pairs added to every span's resource. The SDK reads this; useful for `deployment.environment=staging`, `k8s.namespace.name=...`. |
| `OTEL_TRACES_SAMPLER_ARG` | `0.10` | Root-span sample ratio (0.0–1.0). 10% by default — high enough to spot drift in busy paths, low enough not to flood the collector. Override per-environment. |

### Sampling

Sampling is **parent-based with a `TraceIDRatioBased` root
sampler**. Translation:

- A request that arrives with an inbound `traceparent` header
  inherits the parent's sampling decision. If the upstream caller
  decided to sample the trace, every downstream span is sampled.
  If the upstream decided not to, no span this service produces
  for that request is exported.
- A request without an inbound `traceparent` (i.e. this service
  is the trace root) is sampled at `OTEL_TRACES_SAMPLER_ARG`
  probability.

The 10% default is a starting point. Tune up if you're
investigating a specific issue, tune down if you're
collector-bound.

### Flush behaviour

The tracer provider runs a batch span processor. Spans aren't
exported synchronously; they're queued and flushed in batches
(every few seconds, or when the queue fills). On shutdown,
[`cmd/main.go`](../cmd/main.go) calls the SDK's `Shutdown` with a
5-second deadline so any pending batch reaches the collector
before the process exits.

If you're debugging a missing-trace issue locally, check that:

1. `OTEL_EXPORTER_OTLP_ENDPOINT` is actually set (the no-op
   provider is silent).
2. The collector is reachable (the OTLP gRPC client retries
   internally; check its logs).
3. The trace was sampled — try `OTEL_TRACES_SAMPLER_ARG=1.0`
   in the dev environment to remove sampling as a variable.

## Correlation IDs (DSN-005)

Every request that enters the service flows through
[`api.CorrelationLogger`](../api/middleware.go), which:

1. Reads `X-Request-Id` from the inbound headers (chi's
   `RequestID` middleware honours that header; if absent it
   generates one).
2. Reads the active OTel span from context (`otelchi` runs
   first) and pulls out `trace_id` / `span_id`.
3. Builds a child zerolog logger with `request_id` (and
   `trace_id` / `span_id` when a span is recording) bound, and
   attaches it to the request context.

Downstream code uses `log.Ctx(ctx)` instead of the global
`log.Logger`. That returns the per-request logger when one is
attached, or the global logger as a fallback (the
`zerolog.DefaultContextLogger` pin in
[`core/observability/correlation.go`](../core/observability/correlation.go)
makes the fallback work).

### AMQP propagation

For the queue boundary, the request ID is also stashed on a
private context key by `CorrelationLogger`. The producer reads it
back via `observability.RequestIDFromContext` and writes it to
the AMQP message under the `x-request-id` header. The consumer
reads the header, derives a per-message context with a logger
that carries `request_id`, and invokes the handler with that
context — so logs emitted while processing a queued message tie
back to the original HTTP request that produced it.

DSN-004a additionally propagates W3C `traceparent` (and
`tracestate` / baggage when present) through AMQP headers via a
small `HeaderCarrier` adapter. `amqp.NewMessage` starts a
`PRODUCER` span tagged with `messaging.system=rabbitmq` and the
destination exchange, then injects the trace context into the
outbound headers; the publish loop ends the span on broker
confirm with `OK` on ack and `Error` on nack or publish failure.
The consumer side calls `amqp.StartConsumerSpan` which extracts
the trace context from the delivery headers and starts a
`CONSUMER`-kind span that is a child of the producer's span,
keeping the trace stitched end-to-end. When the producer was
untraced (no `traceparent` on the wire) the consumer span is
still created — just as a root rather than failing.

### What's not (yet) propagated

- **Outbound HTTP** — there is no outbound HTTP client in the
  service today. When one is added, use `otelhttp.Transport`
  for trace propagation; `request_id` can ride along in an
  `X-Request-Id` header lifted from
  `observability.RequestIDFromContext(ctx)`.
