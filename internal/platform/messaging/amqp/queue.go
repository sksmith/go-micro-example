package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// RequestIDHeader is the AMQP message header that carries the inbound
// HTTP request ID across the queue boundary so consumer logs can be
// correlated back to the producing request (DSN-005).
const RequestIDHeader = "x-request-id"

// OTel semantic-convention attribute keys for messaging, kept as
// string constants so a future swap to the semconv package replaces
// them in one place (DSN-004a).
const (
	attrMessagingSystem      = "messaging.system"
	attrMessagingDestination = "messaging.destination.name"
	attrMessagingOperation   = "messaging.operation"
	messagingSystemRabbitMQ  = "rabbitmq"
)

// tracerName is the instrumentation name reported on AMQP producer
// and consumer spans. Kept as a constant so tests that swap the
// global TracerProvider see the same name in the recorded spans.
const tracerName = "github.com/sksmith/go-micro-example/internal/platform/messaging/amqp"

// tracer returns a fresh tracer from the current global provider on
// each call so test setups that swap providers (via
// otel.SetTracerProvider) actually take effect — a package-level
// `var = otel.Tracer(...)` would cache the SDK's tracer at load time
// and bypass the swap.
func tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Message is the application-level payload moved through the broker:
// the body plus correlation metadata the publisher should ferry into
// AMQP headers (and the consumer should pull back out into context).
//
// TraceHeaders carries W3C TraceContext (traceparent/tracestate) and
// any baggage injected by the producer at NewMessage time; the
// publish loop fans them onto the wire alongside x-request-id and
// the consumer extracts them back into a context for span stitching.
//
// producerSpan is the unexported handle the publish loop uses to
// record broker outcome (ack / nack / publish error) — see the End
// calls in Publish. Storing it on the Message lets the span follow
// its message through the goroutine boundary without ctx-threading
// the entire queue subsystem (which TST-003's refactor will tackle).
type Message struct {
	Body         []byte
	RequestID    string
	TraceHeaders map[string]string

	producerSpan trace.Span
}

// NewMessage snapshots correlation fields off ctx so the publisher
// goroutine has what it needs after the request scope has ended,
// starts a producer span tagged with the destination exchange/queue,
// and injects W3C TraceContext into TraceHeaders so the consumer can
// continue the trace. The span ends inside the publish loop on
// broker confirm (ack/nack) or publish error.
func NewMessage(ctx context.Context, body []byte, destination string) Message {
	_, span := tracer().Start(ctx, "amqp.publish "+destination,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String(attrMessagingSystem, messagingSystemRabbitMQ),
			attribute.String(attrMessagingDestination, destination),
			attribute.String(attrMessagingOperation, "publish"),
		),
	)
	headers := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(trace.ContextWithSpan(ctx, span), headers)
	return Message{
		Body:         body,
		RequestID:    observability.RequestIDFromContext(ctx),
		TraceHeaders: map[string]string(headers),
		producerSpan: span,
	}
}

// EncodeEvent wraps a payload in the standard Envelope and serializes
// it. The event_id is a UUID v4 so consumers can use it as the
// idempotency key (DSN-017 / DSN-025 will lean on this).
func EncodeEvent(eventType string, payload any) ([]byte, error) {
	env, err := events.NewEnvelope(uuid.NewString(), eventType, 1, time.Now(), payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

// URL returns the AMQP broker URL derived from cfg.
func URL(cfg *config.Config) string {
	rmq := cfg.RabbitMQ
	return fmt.Sprintf("amqp://%s:%s@%s:%s", rmq.User.Value, rmq.Pass.Value, rmq.Host.Value, rmq.Port.Value)
}

// redialBackoff bounds how long Redial waits between failed dial
// attempts. Kept package-level (not const) so tests can shrink the
// sleep without polluting the production path.
var redialBackoff = 2 * time.Second

// Redial continually connects to url, returning a channel of session
// factories. Each emitted session represents a fresh (connection,
// channel) pair; consumers read sessions sequentially as connections
// drop and recover. The goroutine exits when ctx is cancelled.
func Redial(ctx context.Context, url string) chan chan Session {
	return redialWith(ctx, realDialer(url))
}

// redialWith is the test seam for Redial. The production entry point
// hands a Dialer that talks to a real broker; tests inject a fake
// that scripts the exact sequence of (succeed | fail | dropped)
// outcomes the loop has to handle. Behaviour mirrors Redial 1:1.
//
// Once a consumer has received the sess channel on `sessions`,
// it is committed to receiving exactly one Session value on that
// channel — so retries on dial failure happen *inside* this
// iteration, not by re-offering sess to a fresh consumer (which
// would deadlock with the consumer already parked at `<-session`).
func redialWith(ctx context.Context, dialer Dialer) chan chan Session {
	sessions := make(chan chan Session)

	go func() {
		sess := make(chan Session)
		defer close(sessions)

		for {
			select {
			case sessions <- sess:
			case <-ctx.Done():
				// Normal lifecycle event — the process is shutting
				// down. log.Fatal here would exit the process from
				// inside a goroutine and short-circuit the graceful
				// shutdown path in cmd/main.go.
				log.Info().Msg("shutting down session factory")
				return
			}

			s, ok := dialUntilSuccess(ctx, dialer)
			if !ok {
				// Context cancelled while retrying. The consumer
				// that just took sess will see the channel close
				// alongside `sessions` and exit its outer range.
				return
			}

			select {
			case sess <- s:
			case <-ctx.Done():
				log.Info().Msg("shutting down new session")
				return
			}
		}
	}()

	return sessions
}

// dialUntilSuccess retries dialer with redialBackoff between attempts
// until the broker accepts a (connection, channel) pair or ctx
// cancels. Returning (_, false) signals a context-driven shutdown so
// the caller can unwind cleanly.
func dialUntilSuccess(ctx context.Context, dialer Dialer) (Session, bool) {
	for {
		s, err := dialer(ctx)
		if err == nil {
			return s, true
		}
		log.Error().Err(err).Msg("cannot (re)dial; retrying")
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(redialBackoff):
		}
	}
}

// Publish publishes messages to a reconnecting session against a
// fanout exchange. Messages from messages are consumed and delivered;
// the loop drains and retries on transient failures. onSession is
// called each time a fresh (connection, channel) pair is obtained so
// callers can track liveness for readiness probes (TST-004); nil is
// allowed for callers that don't need the signal.
func Publish(sessions chan chan Session, exchange string, messages <-chan Message, onSession func()) {
	for session := range sessions {
		var (
			running bool
			reading = messages
			pending = make(chan Message, 1)
			confirm = make(chan amqp.Confirmation, 1)
		)

		pub := <-session
		if onSession != nil {
			onSession()
		}

		// publisher confirms for this channel/connection
		if err := pub.Confirm(false); err != nil {
			log.Info().Msg("publisher confirms not supported")
			close(confirm) // confirms not supported, simulate by always nacking
		} else {
			pub.NotifyPublish(confirm)
		}

		log.Debug().Str("exchange", exchange).Msg("ready to publish messages")

		// body must outlive the inner-loop iteration so the confirm
		// arrival a few selects later can still reach the message
		// whose Publish call kicked it off. The pre-refactor loop
		// declared `var body Message` inside the for, which meant
		// every confirm/nack saw a zero-value body — TST-003 spans
		// would never end and the nack log line printed empty bodies.
		var body Message
	Publish:
		for {
			select {
			case confirmed, ok := <-confirm:
				if !ok {
					// Channel/session lost before the broker confirmed
					// the in-flight body. Surface that on the producer
					// span so the trace shows the publish neither
					// succeeded nor was nacked.
					endProducerSpan(body.producerSpan, codes.Error, "broker confirm channel closed", nil)
					break Publish
				}
				if confirmed.Ack {
					endProducerSpan(body.producerSpan, codes.Ok, "", nil)
				} else {
					log.Info().Uint64("message", confirmed.DeliveryTag).Str("body", string(body.Body)).Msg("nack")
					endProducerSpan(body.producerSpan, codes.Error, "broker nacked", nil)
				}
				reading = messages

			case body = <-pending:
				routingKey := "ignored for fanout exchanges, application dependent for other exchanges"
				headers := amqp.Table{}
				if body.RequestID != "" {
					headers[RequestIDHeader] = body.RequestID
				}
				for k, v := range body.TraceHeaders {
					headers[k] = v
				}
				err := pub.Publish(exchange, routingKey, false, false, amqp.Publishing{
					Headers: headers,
					Body:    body.Body,
				})
				// Retry failed delivery on the next session. The
				// producer span ends here with the original error;
				// the retry on the next session is untraced (no fresh
				// span available without ctx-threading the retry).
				if err != nil {
					endProducerSpan(body.producerSpan, codes.Error, "publish failed", err)
					pending <- body
					_ = pub.Close()
					break Publish
				}

			case body, running = <-reading:
				// all messages consumed
				if !running {
					return
				}
				// work on pending delivery until ack'd
				pending <- body
				reading = nil
			}
		}
	}
}

// endProducerSpan closes a producer span if non-nil with the given
// status and optional error. Safe to call on a nil span — handy for
// the never-traced edges where NewMessage wasn't used.
func endProducerSpan(span trace.Span, code codes.Code, description string, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
	}
	if code != codes.Unset {
		span.SetStatus(code, description)
	}
	span.End()
}

// StartConsumerSpan starts a CONSUMER-kind span as a child of the
// trace context carried in msg.TraceHeaders. Returns the new ctx
// and the span; the caller owns ending the span (typically with
// defer span.End()).
//
// queue is the source queue/exchange the message was consumed from
// and lands on the messaging.destination.name attribute. When the
// producer was untraced (no TraceHeaders), the consumer span becomes
// a root span rather than failing — the trace will be missing the
// producer leg but the consumer work is still observable.
func StartConsumerSpan(ctx context.Context, queue string, msg Message) (context.Context, trace.Span) {
	parent := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(msg.TraceHeaders))
	return tracer().Start(parent, "amqp.deliver "+queue,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String(attrMessagingSystem, messagingSystemRabbitMQ),
			attribute.String(attrMessagingDestination, queue),
			attribute.String(attrMessagingOperation, "process"),
		),
	)
}

// Subscribe consumes deliveries from an exclusive queue and forwards
// them onto messages. The request_id header is restored onto the
// Message so context propagation survives the broker hop. onSession
// is called each time a fresh session is obtained and Consume
// succeeds (TST-004); nil is allowed.
func Subscribe(sessions chan chan Session, queue string, messages chan<- Message, onSession func()) {
	for session := range sessions {
		sub := <-session

		deliveries, err := sub.Consume(queue, "", false, false, false, false, nil)
		if err != nil {
			log.Error().Str("queue", queue).Err(err).Msg("cannot consume from")
			return
		}

		if onSession != nil {
			onSession()
		}

		log.Info().Str("queue", queue).Msg("listening for messages")

		for msg := range deliveries {
			reqID := ""
			traceHeaders := map[string]string{}
			for k, v := range msg.Headers {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if k == RequestIDHeader {
					reqID = s
					continue
				}
				traceHeaders[k] = s
			}
			messages <- Message{Body: msg.Body, RequestID: reqID, TraceHeaders: traceHeaders}
			err = sub.Ack(msg.DeliveryTag, false)
			if err != nil {
				log.Error().Err(err).Str("queue", queue).Msg("failed to acknowledge to queue")
			}
		}
	}
}
