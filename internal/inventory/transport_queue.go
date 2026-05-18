package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/messaging/amqp"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"go.opentelemetry.io/otel/codes"
)

// amqpSessionStaleAfter bounds how long a publish/subscribe loop may
// go without obtaining a fresh AMQP session before the queue reports
// unready. Aligns with the DSN-002 readiness budget (DEFAULT 2s
// per-dep) — anything beyond ~10s means redial has stalled and the
// pod should not receive new work (TST-004).
const amqpSessionStaleAfter = 10 * time.Second

// errAMQPNeverConnected signals the publish/subscribe loops have not
// yet obtained a session since process start. Surfaced via Ping so
// /readyz returns 503 during startup before AMQP comes up.
var errAMQPNeverConnected = errors.New("amqp: no session yet")

// InventoryQueue publishes inventory and reservation events onto
// their configured AMQP exchanges. The broker plumbing (publish loop,
// reconnect) lives in internal/platform/messaging/amqp.
//
// lastSessionAt records the most-recent unix-nano timestamp at which
// any of its publish loops obtained a fresh AMQP session, used by
// Ping for /readyz (TST-004).
type InventoryQueue struct {
	cfg           *config.Config
	inventory     chan<- amqp.Message
	reservation   chan<- amqp.Message
	lastSessionAt atomic.Int64
}

func NewInventoryQueue(ctx context.Context, cfg *config.Config) *InventoryQueue {
	invChan := make(chan amqp.Message)
	resChan := make(chan amqp.Message)

	iq := &InventoryQueue{
		cfg:         cfg,
		inventory:   invChan,
		reservation: resChan,
	}

	url := amqp.URL(cfg)

	go func() {
		invExch := cfg.RabbitMQ.Inventory.Exchange.Value
		amqp.Publish(amqp.Redial(ctx, url), invExch, invChan, iq.sessionOK)
		ctx.Done()
	}()

	go func() {
		resExch := cfg.RabbitMQ.Reservation.Exchange.Value
		amqp.Publish(amqp.Redial(ctx, url), resExch, resChan, iq.sessionOK)
		ctx.Done()
	}()

	return iq
}

// sessionOK is invoked by the AMQP publish loops each time they
// successfully open a fresh (connection, channel) pair.
func (i *InventoryQueue) sessionOK() {
	i.lastSessionAt.Store(time.Now().UnixNano())
}

// Ping satisfies app.Pinger. Reports unready until the first session
// arrives and again after amqpSessionStaleAfter without one.
func (i *InventoryQueue) Ping(_ context.Context) error {
	return pingFromLastSession(i.lastSessionAt.Load())
}

func (i *InventoryQueue) PublishInventory(ctx context.Context, productInventory ProductInventory) error {
	body, err := amqp.EncodeEvent(events.TypeProductInventoryChanged, productInventory)
	if err != nil {
		return fmt.Errorf("failed to serialize inventory event: %w", err)
	}
	i.inventory <- amqp.NewMessage(ctx, body, i.cfg.RabbitMQ.Inventory.Exchange.Value)
	return nil
}

func (i *InventoryQueue) PublishReservation(ctx context.Context, reservation Reservation) error {
	body, err := amqp.EncodeEvent(events.TypeReservationChanged, reservation)
	if err != nil {
		return fmt.Errorf("failed to serialize reservation event: %w", err)
	}
	i.reservation <- amqp.NewMessage(ctx, body, i.cfg.RabbitMQ.Reservation.Exchange.Value)
	return nil
}

// ProductQueue consumes inbound product-created events off AMQP and
// dispatches them to a ProductHandler. Invalid envelopes and
// unsupported event types route to the DLT exchange.
//
// lastSessionAt mirrors InventoryQueue's tracking field; the
// subscribe and DLT-publish loops both feed it so Ping reports ready
// only while at least one loop is holding a session (TST-004).
type ProductQueue struct {
	cfg           *config.Config
	product       <-chan amqp.Message
	productDlt    chan<- amqp.Message
	lastSessionAt atomic.Int64
}

func NewProductQueue(ctx context.Context, cfg *config.Config, handler ProductHandler) *ProductQueue {
	log.Info().Msg("creating product queue...")

	prodChan := make(chan amqp.Message)
	prodDltChan := make(chan amqp.Message)

	pq := &ProductQueue{
		cfg:        cfg,
		product:    prodChan,
		productDlt: prodDltChan,
	}

	url := amqp.URL(cfg)

	go func() {
		for msg := range prodChan {
			pq.handleProductMessage(ctx, handler, msg)
		}
	}()

	go func() {
		prodQueue := cfg.RabbitMQ.Product.Queue.Value
		amqp.Subscribe(amqp.Redial(ctx, url), prodQueue, prodChan, pq.sessionOK)
		ctx.Done()
	}()

	go func() {
		dltExch := cfg.RabbitMQ.Product.Dlt.Exchange.Value
		amqp.Publish(amqp.Redial(ctx, url), dltExch, prodDltChan, pq.sessionOK)
		ctx.Done()
	}()

	return pq
}

// sessionOK is invoked by the AMQP subscribe + DLT publish loops each
// time a fresh session is established.
func (p *ProductQueue) sessionOK() {
	p.lastSessionAt.Store(time.Now().UnixNano())
}

// Ping satisfies app.Pinger.
func (p *ProductQueue) Ping(_ context.Context) error {
	return pingFromLastSession(p.lastSessionAt.Load())
}

// pingFromLastSession encodes the InventoryQueue/ProductQueue shared
// readiness rule. Extracted so both queues compute the same thing and
// tests can pin the staleness boundary without touching real time.
func pingFromLastSession(lastNanos int64) error {
	if lastNanos == 0 {
		return errAMQPNeverConnected
	}
	age := time.Since(time.Unix(0, lastNanos))
	if age > amqpSessionStaleAfter {
		return fmt.Errorf("amqp: no session in %s", age.Truncate(time.Second))
	}
	return nil
}

type ProductHandler interface {
	CreateProduct(ctx context.Context, product Product) error
}

func (p *ProductQueue) sendToDlt(ctx context.Context, body []byte) {
	p.productDlt <- amqp.NewMessage(ctx, body, p.cfg.RabbitMQ.Product.Dlt.Exchange.Value)
}

// handleProductMessage validates an incoming product message against
// the events.TypeProductCreated schema and routes invalid messages to
// the DLT with a logged reason. Extracted from NewProductQueue so the
// validation + DLT logic can be unit-tested without standing up AMQP.
func (p *ProductQueue) handleProductMessage(ctx context.Context, handler ProductHandler, msg amqp.Message) {
	msgCtx := observability.ContextWithRequestID(ctx, msg.RequestID)
	logger := log.With().Str("request_id", msg.RequestID).Logger()
	msgCtx = logger.WithContext(msgCtx)

	// DSN-004a: stitch the consumer span onto the producer's trace
	// via the W3C headers the publisher injected. When the producer
	// was untraced, this still creates a root consumer span so the
	// consume work is observable in isolation.
	msgCtx, span := amqp.StartConsumerSpan(msgCtx, p.cfg.RabbitMQ.Product.Queue.Value, msg)
	defer span.End()

	env, err := events.Validate(msg.Body)
	if err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("invalid event, writing to dlt")
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid event")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}
	if env.EventType != events.TypeProductCreated {
		log.Ctx(msgCtx).Error().Str("event_type", env.EventType).Msg("unsupported event type on product queue, writing to dlt")
		span.SetStatus(codes.Error, "unsupported event type")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}

	product := Product{}
	if err := json.Unmarshal(env.Payload, &product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("failed to decode validated payload, writing to dlt")
		span.RecordError(err)
		span.SetStatus(codes.Error, "decode payload")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}

	if err := handler.CreateProduct(msgCtx, product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Str("event_id", env.EventID).Msg("failed to create product, sending to dlt")
		span.RecordError(err)
		span.SetStatus(codes.Error, "handler failure")
		p.productDlt <- msg
	}
}
