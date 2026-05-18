package amqp

import (
	"context"
	"encoding/json"
	"testing"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// withRecordingTracer swaps the global TracerProvider and propagator
// for the duration of a test, returning a SpanRecorder that captures
// every span ended during the test. Restores the previous globals on
// cleanup so tests can run in any order without leaking state.
func withRecordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	return recorder
}

// TestNewMessage_CapturesRequestIDFromContext pins the producer-side
// half of DSN-005's AMQP correlation: NewMessage must snapshot the
// inbound request_id off ctx so the publisher goroutine can attach it
// to the AMQP headers after the request scope has ended.
func TestNewMessage_CapturesRequestIDFromContext(t *testing.T) {
	ctx := observability.ContextWithRequestID(context.Background(), "req-xyz")
	m := NewMessage(ctx, []byte(`{"sku":"abc"}`), "test.exchange")
	if m.RequestID != "req-xyz" {
		t.Errorf("RequestID = %q, want %q", m.RequestID, "req-xyz")
	}
	if string(m.Body) != `{"sku":"abc"}` {
		t.Errorf("Body = %q, want body to be preserved", string(m.Body))
	}
}

func TestNewMessage_NoRequestIDIsEmpty(t *testing.T) {
	m := NewMessage(context.Background(), []byte("hi"), "test.exchange")
	if m.RequestID != "" {
		t.Errorf("expected empty RequestID for ctx without one, got %q", m.RequestID)
	}
}

// TestEncodeEvent_WrapsPayloadInEnvelope pins the DSN-012 producer
// contract: bodies on the publish channels must be schema-conformant
// envelopes carrying event_id, event_type, event_version, occurred_at,
// and producer.
func TestEncodeEvent_WrapsPayloadInEnvelope(t *testing.T) {
	// Use an ad-hoc payload that satisfies the
	// inventory.product_inventory_changed v1 schema (upc, name, sku,
	// available all required). Schema knowledge lives outside this
	// package; tests here just verify EncodeEvent's envelope shape.
	in := map[string]any{
		"sku":       "sku1",
		"upc":       "upc1",
		"name":      "name1",
		"available": 5,
	}
	body, err := EncodeEvent(events.TypeProductInventoryChanged, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := events.Validate(body)
	if err != nil {
		t.Fatalf("encoded payload failed schema validation: %v", err)
	}
	if env.EventType != events.TypeProductInventoryChanged {
		t.Errorf("event_type got=%q", env.EventType)
	}
	if env.EventVersion != 1 {
		t.Errorf("event_version got=%d want=1", env.EventVersion)
	}
	if env.EventID == "" {
		t.Error("event_id should be populated for consumer idempotency")
	}
	if env.OccurredAt.IsZero() {
		t.Error("occurred_at should be populated")
	}
	if env.Producer != events.Producer {
		t.Errorf("producer got=%q want=%q", env.Producer, events.Producer)
	}

	var got map[string]any
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["sku"] != "sku1" || got["upc"] != "upc1" || got["name"] != "name1" {
		t.Errorf("payload round-trip lost fields: got=%+v want=%+v", got, in)
	}
}

// TestHeaderCarrier_RoundTrip pins the carrier contract used by both
// edges: a Set value comes back through Get, Keys lists every entry,
// and non-string entries in the underlying amqp.Table degrade to ""
// rather than panicking (DSN-004a).
func TestHeaderCarrier_RoundTrip(t *testing.T) {
	c := HeaderCarrier(amqp091.Table{"traceparent": "abc", "non-string": 42})

	if got := c.Get("traceparent"); got != "abc" {
		t.Errorf("Get traceparent = %q want %q", got, "abc")
	}
	if got := c.Get("non-string"); got != "" {
		t.Errorf("Get non-string = %q want empty (degrades to absent)", got)
	}
	if got := c.Get("absent"); got != "" {
		t.Errorf("Get absent = %q want empty", got)
	}

	c.Set("tracestate", "vendor=value")
	if got := c.Get("tracestate"); got != "vendor=value" {
		t.Errorf("Set then Get = %q want %q", got, "vendor=value")
	}

	keys := c.Keys()
	if len(keys) != 3 {
		t.Errorf("Keys returned %d entries, want 3", len(keys))
	}
}

// TestNewMessage_InjectsTraceHeaders is the producer-side half of the
// DSN-004a span-stitching contract: NewMessage must (1) snapshot the
// inbound request_id, (2) start a producer-kind span tagged with the
// destination, and (3) inject W3C TraceContext into TraceHeaders so
// the consumer can stitch onto the producer's trace.
func TestNewMessage_InjectsTraceHeaders(t *testing.T) {
	recorder := withRecordingTracer(t)

	ctx := observability.ContextWithRequestID(context.Background(), "req-1")
	msg := NewMessage(ctx, []byte("body"), "inventory.exchange")

	if msg.RequestID != "req-1" {
		t.Errorf("RequestID = %q want %q", msg.RequestID, "req-1")
	}
	if msg.TraceHeaders["traceparent"] == "" {
		t.Errorf("TraceHeaders missing traceparent: %+v", msg.TraceHeaders)
	}
	if msg.producerSpan == nil {
		t.Fatal("producerSpan should be populated for tests that drive endProducerSpan")
	}

	// Span isn't ended yet — it ends in the publish loop. Driving it
	// here proves both the kind and the destination attribute land
	// on the recorded span.
	endProducerSpan(msg.producerSpan, 0, "", nil)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	got := spans[0]
	if got.SpanKind() != trace.SpanKindProducer {
		t.Errorf("span kind = %v want producer", got.SpanKind())
	}
	if got.Name() != "amqp.publish inventory.exchange" {
		t.Errorf("span name = %q want %q", got.Name(), "amqp.publish inventory.exchange")
	}
	attrs := got.Attributes()
	if !containsKV(attrs, attrMessagingSystem, messagingSystemRabbitMQ) {
		t.Errorf("missing messaging.system=rabbitmq attr; got=%v", attrs)
	}
	if !containsKV(attrs, attrMessagingDestination, "inventory.exchange") {
		t.Errorf("missing messaging.destination.name attr; got=%v", attrs)
	}
}

// TestStartConsumerSpan_StitchesOntoProducer is the consumer-side
// half: when TraceHeaders carry a traceparent, StartConsumerSpan
// creates a child span whose parent matches the producer's
// trace+span ID. Without that, every consumer span would be a root
// and producer/consumer traces wouldn't join.
func TestStartConsumerSpan_StitchesOntoProducer(t *testing.T) {
	recorder := withRecordingTracer(t)

	// Stand in for the producer: create a span and inject from its
	// context, exactly the way NewMessage does on the wire.
	prodCtx, prodSpan := otel.Tracer("test").Start(context.Background(), "producer-side")
	headers := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(prodCtx, headers)
	prodSpan.End()

	msg := Message{TraceHeaders: map[string]string(headers)}
	_, consSpan := StartConsumerSpan(context.Background(), "product.queue", msg)
	consSpan.End()

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2 (producer + consumer)", len(spans))
	}

	// Find each by name; iteration order is not guaranteed.
	var producer, consumer sdktrace.ReadOnlySpan
	for _, s := range spans {
		switch s.Name() {
		case "producer-side":
			producer = s
		case "amqp.deliver product.queue":
			consumer = s
		}
	}
	if producer == nil || consumer == nil {
		t.Fatalf("missing expected span; got names=%v", spanNames(spans))
	}
	if consumer.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("consumer span kind = %v want consumer", consumer.SpanKind())
	}
	if producer.SpanContext().TraceID() != consumer.SpanContext().TraceID() {
		t.Errorf("trace IDs differ: producer=%s consumer=%s",
			producer.SpanContext().TraceID(), consumer.SpanContext().TraceID())
	}
	if consumer.Parent().SpanID() != producer.SpanContext().SpanID() {
		t.Errorf("consumer.Parent().SpanID() = %s, want producer.SpanID() = %s",
			consumer.Parent().SpanID(), producer.SpanContext().SpanID())
	}
}

// TestStartConsumerSpan_NoHeadersIsRootSpan covers the degenerate
// case: when the producer was untraced (no TraceHeaders), the
// consumer span should still be created — just as a root rather
// than failing. The consume work stays observable; the trace just
// lacks the producer leg.
func TestStartConsumerSpan_NoHeadersIsRootSpan(t *testing.T) {
	recorder := withRecordingTracer(t)

	_, span := StartConsumerSpan(context.Background(), "product.queue", Message{})
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	got := spans[0]
	if got.Parent().IsValid() {
		t.Errorf("consumer span should be root when producer is untraced; got parent=%+v", got.Parent())
	}
	if got.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("kind = %v want consumer", got.SpanKind())
	}
}

func containsKV(attrs []attribute.KeyValue, key, val string) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key && kv.Value.AsString() == val {
			return true
		}
	}
	return false
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}
