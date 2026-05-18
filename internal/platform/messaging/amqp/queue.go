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
)

// RequestIDHeader is the AMQP message header that carries the inbound
// HTTP request ID across the queue boundary so consumer logs can be
// correlated back to the producing request (DSN-005).
const RequestIDHeader = "x-request-id"

// Message is the application-level payload moved through the broker:
// the body plus correlation metadata the publisher should ferry into
// AMQP headers (and the consumer should pull back out into context).
type Message struct {
	Body      []byte
	RequestID string
}

// NewMessage snapshots correlation fields off ctx so the publisher
// goroutine has what it needs after the request scope has ended.
func NewMessage(ctx context.Context, body []byte) Message {
	return Message{Body: body, RequestID: observability.RequestIDFromContext(ctx)}
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

// session composes an amqp.Connection with an amqp.Channel.
type session struct {
	*amqp.Connection
	*amqp.Channel
}

func (s session) Close() error {
	if s.Connection == nil {
		return nil
	}
	return s.Connection.Close()
}

// Redial continually connects to url, returning a channel of session
// factories. Each emitted session represents a fresh (connection,
// channel) pair; consumers read sessions sequentially as connections
// drop and recover. The goroutine exits when ctx is cancelled.
func Redial(ctx context.Context, url string) chan chan session {
	sessions := make(chan chan session)

	go func() {
		sess := make(chan session)
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

			conn, err := amqp.Dial(url)
			if err != nil {
				log.Error().Err(err).Str("url", url).Msg("cannot (re)dial; retrying")
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}

			ch, err := conn.Channel()
			if err != nil {
				log.Error().Err(err).Msg("cannot create channel; retrying")
				_ = conn.Close()
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}

			select {
			case sess <- session{conn, ch}:
			case <-ctx.Done():
				log.Info().Msg("shutting down new session")
				return
			}
		}
	}()

	return sessions
}

// Publish publishes messages to a reconnecting session against a
// fanout exchange. Messages from messages are consumed and delivered;
// the loop drains and retries on transient failures.
func Publish(sessions chan chan session, exchange string, messages <-chan Message) {
	for session := range sessions {
		var (
			running bool
			reading = messages
			pending = make(chan Message, 1)
			confirm = make(chan amqp.Confirmation, 1)
		)

		pub := <-session

		// publisher confirms for this channel/connection
		if err := pub.Confirm(false); err != nil {
			log.Info().Msg("publisher confirms not supported")
			close(confirm) // confirms not supported, simulate by always nacking
		} else {
			pub.NotifyPublish(confirm)
		}

		log.Debug().Str("exchange", exchange).Msg("ready to publish messages")

	Publish:
		for {
			var body Message
			select {
			case confirmed, ok := <-confirm:
				if !ok {
					break Publish
				}
				if !confirmed.Ack {
					log.Info().Uint64("message", confirmed.DeliveryTag).Str("body", string(body.Body)).Msg("nack")
				}
				reading = messages

			case body = <-pending:
				routingKey := "ignored for fanout exchanges, application dependent for other exchanges"
				headers := amqp.Table{}
				if body.RequestID != "" {
					headers[RequestIDHeader] = body.RequestID
				}
				err := pub.Publish(exchange, routingKey, false, false, amqp.Publishing{
					Headers: headers,
					Body:    body.Body,
				})
				// Retry failed delivery on the next session
				if err != nil {
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

// Subscribe consumes deliveries from an exclusive queue and forwards
// them onto messages. The request_id header is restored onto the
// Message so context propagation survives the broker hop.
func Subscribe(sessions chan chan session, queue string, messages chan<- Message) {
	for session := range sessions {
		sub := <-session

		deliveries, err := sub.Consume(queue, "", false, false, false, false, nil)
		if err != nil {
			log.Error().Str("queue", queue).Err(err).Msg("cannot consume from")
			return
		}

		log.Info().Str("queue", queue).Msg("listening for messages")

		for msg := range deliveries {
			reqID := ""
			if v, ok := msg.Headers[RequestIDHeader].(string); ok {
				reqID = v
			}
			messages <- Message{Body: msg.Body, RequestID: reqID}
			err = sub.Ack(msg.DeliveryTag, false)
			if err != nil {
				log.Error().Err(err).Str("queue", queue).Msg("failed to acknowledge to queue")
			}
		}
	}
}
