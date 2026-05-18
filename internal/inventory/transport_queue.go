package inventory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/messaging/amqp"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
)

// InventoryQueue publishes inventory and reservation events onto
// their configured AMQP exchanges. The broker plumbing (publish loop,
// reconnect) lives in internal/platform/messaging/amqp.
type InventoryQueue struct {
	cfg         *config.Config
	inventory   chan<- amqp.Message
	reservation chan<- amqp.Message
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
		amqp.Publish(amqp.Redial(ctx, url), invExch, invChan)
		ctx.Done()
	}()

	go func() {
		resExch := cfg.RabbitMQ.Reservation.Exchange.Value
		amqp.Publish(amqp.Redial(ctx, url), resExch, resChan)
		ctx.Done()
	}()

	return iq
}

func (i *InventoryQueue) PublishInventory(ctx context.Context, productInventory ProductInventory) error {
	body, err := amqp.EncodeEvent(events.TypeProductInventoryChanged, productInventory)
	if err != nil {
		return fmt.Errorf("failed to serialize inventory event: %w", err)
	}
	i.inventory <- amqp.NewMessage(ctx, body)
	return nil
}

func (i *InventoryQueue) PublishReservation(ctx context.Context, reservation Reservation) error {
	body, err := amqp.EncodeEvent(events.TypeReservationChanged, reservation)
	if err != nil {
		return fmt.Errorf("failed to serialize reservation event: %w", err)
	}
	i.reservation <- amqp.NewMessage(ctx, body)
	return nil
}

// ProductQueue consumes inbound product-created events off AMQP and
// dispatches them to a ProductHandler. Invalid envelopes and
// unsupported event types route to the DLT exchange.
type ProductQueue struct {
	cfg        *config.Config
	product    <-chan amqp.Message
	productDlt chan<- amqp.Message
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
		amqp.Subscribe(amqp.Redial(ctx, url), prodQueue, prodChan)
		ctx.Done()
	}()

	go func() {
		dltExch := cfg.RabbitMQ.Product.Dlt.Exchange.Value
		amqp.Publish(amqp.Redial(ctx, url), dltExch, prodDltChan)
		ctx.Done()
	}()

	return pq
}

type ProductHandler interface {
	CreateProduct(ctx context.Context, product Product) error
}

func (p *ProductQueue) sendToDlt(ctx context.Context, body []byte) {
	p.productDlt <- amqp.NewMessage(ctx, body)
}

// handleProductMessage validates an incoming product message against
// the events.TypeProductCreated schema and routes invalid messages to
// the DLT with a logged reason. Extracted from NewProductQueue so the
// validation + DLT logic can be unit-tested without standing up AMQP.
func (p *ProductQueue) handleProductMessage(ctx context.Context, handler ProductHandler, msg amqp.Message) {
	msgCtx := observability.ContextWithRequestID(ctx, msg.RequestID)
	logger := log.With().Str("request_id", msg.RequestID).Logger()
	msgCtx = logger.WithContext(msgCtx)

	env, err := events.Validate(msg.Body)
	if err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("invalid event, writing to dlt")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}
	if env.EventType != events.TypeProductCreated {
		log.Ctx(msgCtx).Error().Str("event_type", env.EventType).Msg("unsupported event type on product queue, writing to dlt")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}

	product := Product{}
	if err := json.Unmarshal(env.Payload, &product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Msg("failed to decode validated payload, writing to dlt")
		p.sendToDlt(msgCtx, msg.Body)
		return
	}

	if err := handler.CreateProduct(msgCtx, product); err != nil {
		log.Ctx(msgCtx).Error().Err(err).Str("event_id", env.EventID).Msg("failed to create product, sending to dlt")
		p.productDlt <- msg
	}
}
