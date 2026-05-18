package inventory

import (
	"context"
)

type MockQueue struct {
	PublishInventoryFunc   func(ctx context.Context, productInventory ProductInventory) error
	PublishReservationFunc func(ctx context.Context, reservation Reservation) error

	PublishInventoryCalls   int
	PublishReservationCalls int
}

func NewMockQueue() *MockQueue {
	return &MockQueue{
		PublishInventoryFunc: func(ctx context.Context, productInventory ProductInventory) error {
			return nil
		},
		PublishReservationFunc: func(ctx context.Context, reservation Reservation) error {
			return nil
		},
	}
}

func (m *MockQueue) PublishInventory(ctx context.Context, productInventory ProductInventory) error {
	m.PublishInventoryCalls++
	return m.PublishInventoryFunc(ctx, productInventory)
}

func (m *MockQueue) PublishReservation(ctx context.Context, reservation Reservation) error {
	m.PublishReservationCalls++
	return m.PublishReservationFunc(ctx, reservation)
}
