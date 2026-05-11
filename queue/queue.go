package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/observability"
	"github.com/sksmith/go-micro-example/events"
)

// AMQPRequestIDHeader is the AMQP message header that carries the
// inbound HTTP request ID across the queue boundary so consumer logs
// can be correlated back to the producing request (DSN-005).
const AMQPRequestIDHeader = "x-request-id"

type InventoryQueue struct {
	cfg         *config.Config
	inventory   chan<- message
	reservation chan<- message
}

func NewInventoryQueue(ctx context.Context, cfg *config.Config) *InventoryQueue {
	invChan := make(chan message)
	resChan := make(chan message)

	iq := &InventoryQueue{
		cfg:         cfg,
		inventory:   invChan,
		reservation: resChan,
	}

	url := getUrl(cfg)

	go func() {
		invExch := cfg.RabbitMQ.Inventory.Exchange.Value
		publish(redial(ctx, url), invExch, invChan)
		ctx.Done()
	}()

	go func() {
		resExch := cfg.RabbitMQ.Reservation.Exchange.Value
		publish(redial(ctx, url), resExch, resChan)
		ctx.Done()
	}()

	return iq
}

func getUrl(cfg *config.Config) string {
	rmq := cfg.RabbitMQ
	return fmt.Sprintf("amqp://%s:%s@%s:%s", rmq.User.Value, rmq.Pass.Value, rmq.Host.Value, rmq.Port.Value)
}

func (i *InventoryQueue) PublishInventory(ctx context.Context, productInventory inventory.ProductInventory) error {
	body, err := encodeEvent(events.TypeProductInventoryChanged, productInventory)
	if err != nil {
		return fmt.Errorf("failed to serialize inventory event: %w", err)
	}
	i.inventory <- newMessage(ctx, body)
	return nil
}

func (i *InventoryQueue) PublishReservation(ctx context.Context, reservation inventory.Reservation) error {
	body, err := encodeEvent(events.TypeReservationChanged, reservation)
	if err != nil {
		return fmt.Errorf("failed to serialize reservation event: %w", err)
	}
	i.reservation <- newMessage(ctx, body)
	return nil
}

// encodeEvent wraps a payload in the standard Envelope and serializes
// it. The event_id is a UUID v4 so consumers can use it as the
// idempotency key (DSN-017 / DSN-025 will lean on this).
func encodeEvent(eventType string, payload any) ([]byte, error) {
	env, err := events.NewEnvelope(uuid.NewString(), eventType, 1, time.Now(), payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(env)
}

// newMessage snapshots the correlation fields off ctx so the publisher
// goroutine has what it needs after the request scope has ended.
func newMessage(ctx context.Context, body []byte) message {
	return message{body: body, requestID: observability.RequestIDFromContext(ctx)}
}

type ProductQueue struct {
	cfg        *config.Config
	product    <-chan message
	productDlt chan<- message
}

func NewProductQueue(ctx context.Context, cfg *config.Config, handler ProductHandler) *ProductQueue {
	log.Info().Msg("creating product queue...")

	prodChan := make(chan message)
	prodDltChan := make(chan message)

	pq := &ProductQueue{
		cfg:        cfg,
		product:    prodChan,
		productDlt: prodDltChan,
	}

	url := getUrl(cfg)

	go func() {
		for msg := range prodChan {
			pq.handleProductMessage(ctx, handler, msg)
		}
	}()

	go func() {
		prodQueue := cfg.RabbitMQ.Product.Queue.Value
		subscribe(redial(ctx, url), prodQueue, prodChan)
		ctx.Done()
	}()

	go func() {
		dltExch := cfg.RabbitMQ.Product.Dlt.Exchange.Value
		publish(redial(ctx, url), dltExch, prodDltChan)
		ctx.Done()
	}()

	return pq
}

type ProductHandler interface {
	CreateProduct(ctx context.Context, product inventory.Product) error
}

func (p *ProductQueue) sendToDlt(ctx context.Context, body []byte) {
	p.productDlt <- newMessage(ctx, body)
}

// handleProductMessage validates an incoming product message against
// the events.TypeProductCreated schema and routes invalid messages to
// the DLT with a logged reason. Extracted from NewProductQueue so the
// validation + DLT logic can be unit-tested without standing up AMQP.
func (p *ProductQueue) handleProductMessage(ctx context.Context, handler ProductHandler, msg message) {
	msgCtx := observability.ContextWithRequestID(ctx, msg.requestID)
	logger := log.With().Str("request_id", msg.requestID).Logger()
	msgCtx = logger.WithContext(msgCtx)

	env, err := events.Validate(msg.body)
	if err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("invalid event, writing to dlt")
		p.sendToDlt(msgCtx, msg.body)
		return
	}
	if env.EventType != events.TypeProductCreated {
		log.Ctx(msgCtx).Error().Str("event_type", env.EventType).Msg("unsupported event type on product queue, writing to dlt")
		p.sendToDlt(msgCtx, msg.body)
		return
	}

	product := inventory.Product{}
	if err := json.Unmarshal(env.Payload, &product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("failed to decode validated payload, writing to dlt")
		p.sendToDlt(msgCtx, msg.body)
		return
	}

	if err := handler.CreateProduct(msgCtx, product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Str("event_id", env.EventID).Msg("failed to create product, sending to dlt")
		p.productDlt <- msg
	}
}

// TODO We should be using one exchange per domain object here.
// exchange binds the publishers to the subscribers
// const exchange = "pubsub"

// message is the application type for a message — the body plus any
// correlation metadata the publisher should ferry into AMQP headers
// (and the consumer should pull back out into context).
type message struct {
	body      []byte
	requestID string
}

// session composes an amqp.Connection with an amqp.Channel
type session struct {
	*amqp.Connection
	*amqp.Channel
}

// Close tears the connection down, taking the channel with it.
func (s session) Close() error {
	if s.Connection == nil {
		return nil
	}
	return s.Connection.Close()
}

// redial continually connects to the URL, exiting the program when no longer possible
func redial(ctx context.Context, url string) chan chan session {
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

// publish publishes messages to a reconnecting session to a fanout exchange.
// It receives from the application specific source of messages.
func publish(sessions chan chan session, exchange string, messages <-chan message) {
	for session := range sessions {
		var (
			running bool
			reading = messages
			pending = make(chan message, 1)
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
			var body message
			select {
			case confirmed, ok := <-confirm:
				if !ok {
					break Publish
				}
				if !confirmed.Ack {
					log.Info().Uint64("message", confirmed.DeliveryTag).Str("body", string(body.body)).Msg("nack")
				}
				reading = messages

			case body = <-pending:
				routingKey := "ignored for fanout exchanges, application dependent for other exchanges"
				headers := amqp.Table{}
				if body.requestID != "" {
					headers[AMQPRequestIDHeader] = body.requestID
				}
				err := pub.Publish(exchange, routingKey, false, false, amqp.Publishing{
					Headers: headers,
					Body:    body.body,
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

// subscribe consumes deliveries from an exclusive queue from a fanout exchange and sends to the application specific messages chan.
func subscribe(sessions chan chan session, queue string, messages chan<- message) {
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
			if v, ok := msg.Headers[AMQPRequestIDHeader].(string); ok {
				reqID = v
			}
			messages <- message{body: msg.Body, requestID: reqID}
			err = sub.Ack(msg.DeliveryTag, false)
			if err != nil {
				log.Error().Err(err).Str("queue", queue).Msg("failed to acknowledge to queue")
			}
		}
	}
}
