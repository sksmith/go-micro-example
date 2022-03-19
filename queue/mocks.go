package queue

import (
	"context"

	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/testutil"
)

type MockQueue struct {
	PublishInventoryFunc   func(ctx context.Context, productInventory inventory.ProductInventory) error
	PublishReservationFunc func(ctx context.Context, reservation inventory.Reservation) error
	testutil.CallWatcher
}

func NewMockQueue() *MockQueue {
	return &MockQueue{
		PublishInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory) error {
			return nil
		},
		PublishReservationFunc: func(ctx context.Context, reservation inventory.Reservation) error {
			return nil
		},
		CallWatcher: *testutil.NewCallWatcher(),
	}
}

func (m *MockQueue) PublishInventory(ctx context.Context, productInventory inventory.ProductInventory) error {
	m.AddCall(ctx, productInventory)
	return m.PublishInventoryFunc(ctx, productInventory)
}

func (m *MockQueue) PublishReservation(ctx context.Context, reservation inventory.Reservation) error {
	m.AddCall(ctx, reservation)
	return m.PublishReservationFunc(ctx, reservation)
}
