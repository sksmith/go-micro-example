package queue

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/bunnyq"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/streadway/amqp"
)

type inventoryQueue struct {
	queue               *bunnyq.BunnyQ
	inventoryExchange   string
	reservationExchange string
}

func New(bq *bunnyq.BunnyQ, inventoryExchange, reservationExchange string) inventory.Queue {
	return &inventoryQueue{queue: bq, inventoryExchange: inventoryExchange, reservationExchange: reservationExchange}
}

func (i *inventoryQueue) PublishInventory(ctx context.Context, productInventory inventory.ProductInventory) error {
	body, err := json.Marshal(productInventory)
	if err != nil {
		return errors.WithMessage(err, "failed to serialize message for queue")
	}
	if err = i.queue.Publish(ctx, i.inventoryExchange, body); err != nil {
		return errors.WithMessage(err, "failed to send inventory update to queue")
	}
	return nil
}

func (i *inventoryQueue) PublishReservation(ctx context.Context, reservation inventory.Reservation) error {
	body, err := json.Marshal(reservation)
	if err != nil {
		return errors.WithMessage(err, "error marshalling reservation to send to queue")
	}
	err = i.queue.Publish(ctx, i.reservationExchange, body)
	if err != nil {
		return errors.WithMessage(err, "error publishing reservation")
	}
	return nil
}

type ProductQueue struct {
	queue                 *bunnyq.BunnyQ
	newProductQueue       string
	newProductDltExchange string
}

func NewProductQueue(bq *bunnyq.BunnyQ, newProductQueue, newProductDltExchange string) *ProductQueue {
	return &ProductQueue{queue: bq, newProductDltExchange: newProductDltExchange}
}

type ProductHandler interface {
	CreateProduct(ctx context.Context, product inventory.Product) error
}

func (p *ProductQueue) ConsumeProducts(ctx context.Context, handler ProductHandler) {
	p.queue.Stream(ctx, p.newProductQueue, func(delivery amqp.Delivery) {
		product := inventory.Product{}
		err := json.Unmarshal(delivery.Body, &product)
		if err != nil {
			log.Error().Err(err).Msg("error unmarshalling product, writing to dlt")
			p.sendToDlt(ctx, delivery.Body)
		}

		err = handler.CreateProduct(ctx, product)
		if err != nil {
			log.Error().Err(err).Msg("error handling product, writing to dlt")
			p.sendToDlt(ctx, delivery.Body)
		}
	}, bunnyq.StreamOpAutoAck)
}

func (p *ProductQueue) sendToDlt(ctx context.Context, data []byte) {
	err := p.queue.Publish(ctx, p.newProductDltExchange, data)
	if err != nil {
		log.Error().Err(err).Msg("error writing to dlt")
	}
}
