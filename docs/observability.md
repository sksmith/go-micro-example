# Observability

This service emits two complementary telemetry streams:

| Stream      | Transport                  | Where it shows up                                   |
|-------------|----------------------------|-----------------------------------------------------|
| **Metrics** | Prometheus scrape          | `GET /metrics` (Prometheus text format)             |
| **Traces**  | OpenTelemetry OTLP/gRPC    | Whatever collector `OTEL_EXPORTER_OTLP_ENDPOINT` points at |

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

### What's not yet instrumented

- **AMQP producer/consumer** — the queue subsystem doesn't yet
  expose hook points. Tracked alongside TST-003 / TST-004.
- **Service-layer custom spans** (e.g. `inventory.Service.Reserve`).
  The chi+pgx auto-instrumentation already covers ~80% of the
  signal a typical request needs; explicit business-operation
  spans are a follow-up. File a follow-up ticket if traces
  surface a gap that the auto-instrumentation can't fill.

### Configuration

Three env vars drive the tracing setup; all are read by the OTel
SDK natively (no custom parsing):

| Variable                          | Default | What it does |
|-----------------------------------|---------|--------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT`     | unset   | If unset, tracing installs a no-op provider — call sites still `tracer.Start()` freely, the spans go nowhere. **No collector is required for local dev.** When set, OTLP/gRPC connects to the given endpoint (e.g. `http://otel-collector:4317`). |
| `OTEL_RESOURCE_ATTRIBUTES`        | unset   | Comma-separated `k=v` pairs added to every span's resource. The SDK reads this; useful for `deployment.environment=staging`, `k8s.namespace.name=...`. |
| `OTEL_TRACES_SAMPLER_ARG`         | `0.10`  | Root-span sample ratio (0.0–1.0). 10% by default — high enough to spot drift in busy paths, low enough not to flood the collector. Override per-environment. |

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
