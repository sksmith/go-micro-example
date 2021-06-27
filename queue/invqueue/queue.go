package invqueue

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
	"github.com/sksmith/bunnyq"
	"github.com/sksmith/go-micro-example/core/inventory"
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
