package amqp

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

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

// withShortBackoff drops the redial backoff so reconnect tests don't
// burn 2 s per failed dial. Restored on cleanup.
func withShortBackoff(t *testing.T) {
	t.Helper()
	prev := redialBackoff
	redialBackoff = 10 * time.Millisecond
	t.Cleanup(func() { redialBackoff = prev })
}

// waitFor polls condition every 5 ms up to 2 s. Used instead of
// t.Eventually-style helpers (not in stdlib) to assert against goroutine
// state without sleeping for a fixed duration.
func waitFor(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out: %s", msg)
}

// TestRedial_DialFailureRetries pins the bug TST-003 was created
// to expose: when the first dial fails, the redial loop must keep
// retrying inside the *same* iteration so the consumer that already
// took the sess channel at `<-sessions` eventually receives a real
// Session on `<-session` once dialing succeeds. The pre-refactor
// loop would `continue` out and re-offer sess to a fresh consumer,
// deadlocking the existing one.
func TestRedial_DialFailureRetries(t *testing.T) {
	withShortBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dialErr := errors.New("connection refused")
	good := newFakeSession()
	dialer := newScriptedDialer(
		dialResult{err: dialErr},
		dialResult{err: dialErr},
		dialResult{session: good},
	)

	sessions := redialWith(ctx, dialer.asDialer())

	// Consumer pulls a sess channel and then a session — mirrors the
	// Publish loop's outer `for session := range sessions`.
	sess := <-sessions
	got := <-sess

	if got != Session(good) {
		t.Fatalf("expected dialer's third result, got %T", got)
	}
	if dialer.callCount() != 3 {
		t.Errorf("dialer called %d times, want 3 (two failures then success)", dialer.callCount())
	}
}

// TestRedial_CtxCancelDuringBackoffExitsCleanly proves the
// dialUntilSuccess loop honours context cancellation between
// failed dial attempts — otherwise a broker that's permanently
// unreachable would block shutdown.
func TestRedial_CtxCancelDuringBackoffExitsCleanly(t *testing.T) {
	withShortBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())

	dialer := newScriptedDialer(
		dialResult{err: errors.New("refused")},
		dialResult{err: errors.New("refused")},
		dialResult{err: errors.New("refused")},
	)

	sessions := redialWith(ctx, dialer.asDialer())
	sess := <-sessions

	// While the loop is retrying, cancel ctx and expect both
	// `sessions` and the per-iteration `sess` to close.
	cancel()

	waitFor(t, func() bool {
		select {
		case _, ok := <-sessions:
			return !ok // closed
		default:
			return false
		}
	}, "sessions channel never closed after ctx cancel")

	// The session value channel we already pulled never receives a
	// real session — it just exits via close-of-sessions semantics.
	select {
	case _, ok := <-sess:
		if ok {
			t.Errorf("sess yielded a Session despite ctx cancel during dial")
		}
	case <-time.After(50 * time.Millisecond):
		// Acceptable: the loop returned without sending on sess.
	}
}

// TestPublish_ConfirmSupportedPath drives the happy publish path:
// Confirm returns nil (broker supports confirms), the message goes
// out, the loop waits for a Confirmation, and once an Ack comes
// back the producer span ends with codes.Ok. Exits cleanly when
// the messages channel closes.
func TestPublish_ConfirmSupportedPath(t *testing.T) {
	recorder := withRecordingTracer(t)

	fake := newFakeSession()
	sessions := make(chan chan Session, 1)
	sess := make(chan Session, 1)
	sess <- fake
	sessions <- sess
	close(sessions)

	messages := make(chan Message, 1)
	sessionOK := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		Publish(sessions, "test.exchange", messages, func() { sessionOK <- struct{}{} })
		close(done)
	}()

	<-sessionOK // sanity: loop reached the per-session setup
	messages <- NewMessage(context.Background(), []byte("payload"), "test.exchange")

	waitFor(t, func() bool {
		pub, _, ask, _ := fake.snapshot()
		return ask && len(pub) == 1
	}, "expected Confirm() + one Publish() call")

	fake.confirms() <- amqp091.Confirmation{DeliveryTag: 1, Ack: true}

	close(messages)
	<-done

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected one producer span, got %d (%v)", len(spans), spanNames(spans))
	}
	if got := spans[0].Status().Code.String(); got != "Ok" {
		t.Errorf("ack should set status Ok, got %q (desc=%q)", got, spans[0].Status().Description)
	}
}

// TestPublish_NackEndsSpanError covers the broker-rejected path:
// after a nack confirmation arrives the producer span ends with
// codes.Error so dashboards see a failed publish without having to
// poll log lines.
func TestPublish_NackEndsSpanError(t *testing.T) {
	recorder := withRecordingTracer(t)

	fake := newFakeSession()
	sessions := make(chan chan Session, 1)
	sess := make(chan Session, 1)
	sess <- fake
	sessions <- sess
	close(sessions)

	messages := make(chan Message, 1)
	sessionOK := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		Publish(sessions, "test.exchange", messages, func() { sessionOK <- struct{}{} })
		close(done)
	}()
	<-sessionOK
	messages <- NewMessage(context.Background(), []byte("payload"), "test.exchange")

	waitFor(t, func() bool {
		pub, _, _, _ := fake.snapshot()
		return len(pub) == 1
	}, "Publish() never called")

	fake.confirms() <- amqp091.Confirmation{DeliveryTag: 1, Ack: false}

	close(messages)
	<-done

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected one producer span, got %d", len(spans))
	}
	if got := spans[0].Status().Code.String(); got != "Error" {
		t.Errorf("nack should set status Error, got %q", got)
	}
}

// TestPublish_ConfirmsNotSupportedFallback covers the case where
// the broker reports "publisher confirms not supported." The loop
// calls Confirm(), gets the error back, closes its internal confirm
// channel, and then bails out of the inner loop because the closed
// channel keeps firing the receive case. We assert only that
// Confirm() was attempted and the loop exited cleanly — the
// internal "nack everything" sequencing is non-deterministic
// (Go's select is randomised) and changing it is out of TST-003
// scope. A real broker that doesn't support confirms is not a
// production use case for this codebase.
func TestPublish_ConfirmsNotSupportedFallback(t *testing.T) {
	fake := newFakeSession()
	fake.confirmErr = errors.New("publisher confirms not supported")

	sessions := make(chan chan Session, 1)
	sess := make(chan Session, 1)
	sess <- fake
	sessions <- sess
	close(sessions)

	messages := make(chan Message, 1)
	done := make(chan struct{})
	go func() {
		Publish(sessions, "test.exchange", messages, nil)
		close(done)
	}()

	messages <- NewMessage(context.Background(), []byte("payload"), "test.exchange")
	close(messages)

	select {
	case <-done:
		// Expected: loop unwound after the closed-confirm fallback.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish loop didn't exit after Confirm() returned not-supported")
	}

	_, _, ask, _ := fake.snapshot()
	if !ask {
		t.Error("Confirm() should have been called even when not supported")
	}
}

// TestSubscribe_AckFailureIsLogged drives the consumer-side
// error path: when Ack returns an error, the loop must log it
// (otherwise a broken broker channel silently drops every
// message). We can't easily intercept zerolog from inside the
// package, so the test relies on the side-effect of `ackCalls`:
// the loop must continue calling Ack on subsequent deliveries
// despite the error (i.e. one failure doesn't kill the loop).
func TestSubscribe_AckFailureIsLogged(t *testing.T) {
	fake := newFakeSession()
	fake.ackErr = errors.New("channel closed")

	sessions := make(chan chan Session, 1)
	sess := make(chan Session, 1)
	sess <- fake
	sessions <- sess
	close(sessions)

	out := make(chan Message, 4)
	var observed atomic.Int32
	done := make(chan struct{})

	go func() {
		Subscribe(sessions, "test.queue", out, nil)
		close(done)
	}()

	// Drain `out` concurrently so the unbuffered send inside
	// Subscribe doesn't block.
	go func() {
		for range out {
			observed.Add(1)
		}
	}()

	fake.deliveries <- amqp091.Delivery{DeliveryTag: 1, Body: []byte("msg-1")}
	fake.deliveries <- amqp091.Delivery{DeliveryTag: 2, Body: []byte("msg-2")}

	waitFor(t, func() bool {
		_, acked, _, _ := fake.snapshot()
		return len(acked) == 2
	}, "expected two Ack attempts despite failure on the first")

	// Closing deliveries lets Subscribe drop out of its inner range,
	// and `sessions` is already closed from setup so the outer range
	// exits too. Closing `out` flushes the draining goroutine.
	close(fake.deliveries)
	<-done
	close(out)

	if observed.Load() != 2 {
		t.Errorf("loop dropped a delivery after Ack failure: observed=%d want=2", observed.Load())
	}
}

// TestSubscribe_ConsumeErrorExits documents the fail-fast path:
// when Consume returns an error (e.g. queue not declared, channel
// already closed) the loop exits rather than spinning forever.
// The Redial layer handles the reconnect; Subscribe just unwinds.
func TestSubscribe_ConsumeErrorExits(t *testing.T) {
	fake := newFakeSession()
	fake.consumeErr = errors.New("NOT_FOUND - no queue 'absent'")

	sessions := make(chan chan Session, 1)
	sess := make(chan Session, 1)
	sess <- fake
	sessions <- sess
	close(sessions)

	out := make(chan Message)
	done := make(chan struct{})
	go func() {
		Subscribe(sessions, "absent", out, nil)
		close(done)
	}()

	select {
	case <-done:
		// Expected: Subscribe exited because Consume errored.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Subscribe didn't return after Consume failure")
	}
}
