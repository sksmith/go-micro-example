package invqueue

import (
	"context"

	"github.com/sksmith/go-micro-example/core/inventory"
)

type MockQueue struct {
	PublishInventoryFunc   func(ctx context.Context, productInventory inventory.ProductInventory) error
	PublishReservationFunc func(ctx context.Context, reservation inventory.Reservation) error
}

func NewMockQueue() *MockQueue {
	return &MockQueue{
		PublishInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory) error {
			return nil
		},
		PublishReservationFunc: func(ctx context.Context, reservation inventory.Reservation) error {
			return nil
		},
	}
}

func (m *MockQueue) PublishInventory(ctx context.Context, productInventory inventory.ProductInventory) error {
	return m.PublishInventoryFunc(ctx, productInventory)
}

func (m *MockQueue) PublishReservation(ctx context.Context, reservation inventory.Reservation) error {
	return m.PublishReservationFunc(ctx, reservation)
}
