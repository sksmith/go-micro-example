package inventory

import (
	"context"

	"github.com/sksmith/go-micro-example/testutil"
)

type MockInventoryService struct {
	ProduceFunc                func(ctx context.Context, product Product, event ProductionRequest) error
	CreateProductFunc          func(ctx context.Context, product Product) error
	GetProductFunc             func(ctx context.Context, sku string) (Product, error)
	GetAllProductInventoryFunc func(ctx context.Context, limit, offset int) ([]ProductInventory, error)
	GetProductInventoryFunc    func(ctx context.Context, sku string) (ProductInventory, error)
	SubscribeInventoryFunc     func(ch chan<- ProductInventory) (id InventorySubID)
	UnsubscribeInventoryFunc   func(id InventorySubID)
	*testutil.CallWatcher
}

func NewMockInventoryService() *MockInventoryService {
	return &MockInventoryService{
		ProduceFunc:       func(ctx context.Context, product Product, event ProductionRequest) error { return nil },
		CreateProductFunc: func(ctx context.Context, product Product) error { return nil },
		GetProductFunc:    func(ctx context.Context, sku string) (Product, error) { return Product{}, nil },
		GetAllProductInventoryFunc: func(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
			return []ProductInventory{}, nil
		},
		GetProductInventoryFunc:  func(ctx context.Context, sku string) (ProductInventory, error) { return ProductInventory{}, nil },
		SubscribeInventoryFunc:   func(ch chan<- ProductInventory) (id InventorySubID) { return "" },
		UnsubscribeInventoryFunc: func(id InventorySubID) {},
		CallWatcher:              testutil.NewCallWatcher(),
	}
}

func (i *MockInventoryService) Produce(ctx context.Context, product Product, event ProductionRequest) error {
	i.AddCall(ctx, product, event)
	return i.ProduceFunc(ctx, product, event)
}

func (i *MockInventoryService) CreateProduct(ctx context.Context, product Product) error {
	i.AddCall(ctx, product)
	return i.CreateProductFunc(ctx, product)
}

func (i *MockInventoryService) GetProduct(ctx context.Context, sku string) (Product, error) {
	i.AddCall(ctx, sku)
	return i.GetProductFunc(ctx, sku)
}

func (i *MockInventoryService) GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
	i.AddCall(ctx, limit, offset)
	return i.GetAllProductInventoryFunc(ctx, limit, offset)
}

func (i *MockInventoryService) GetProductInventory(ctx context.Context, sku string) (ProductInventory, error) {
	i.AddCall(ctx, sku)
	return i.GetProductInventoryFunc(ctx, sku)
}

func (i *MockInventoryService) SubscribeInventory(ch chan<- ProductInventory) (id InventorySubID) {
	i.AddCall(ch)
	return i.SubscribeInventoryFunc(ch)
}

func (i *MockInventoryService) UnsubscribeInventory(id InventorySubID) {
	i.AddCall(id)
	i.UnsubscribeInventoryFunc(id)
}

type MockReservationService struct {
	ReserveFunc func(ctx context.Context, rr ReservationRequest) (Reservation, error)

	GetReservationsFunc func(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error)
	GetReservationFunc  func(ctx context.Context, ID uint64) (Reservation, error)

	SubscribeReservationsFunc   func(ch chan<- Reservation) (id ReservationsSubID)
	UnsubscribeReservationsFunc func(id ReservationsSubID)
	*testutil.CallWatcher
}

func NewMockReservationService() *MockReservationService {
	return &MockReservationService{
		ReserveFunc: func(ctx context.Context, rr ReservationRequest) (Reservation, error) { return Reservation{}, nil },
		GetReservationsFunc: func(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error) {
			return []Reservation{}, nil
		},
		GetReservationFunc:          func(ctx context.Context, ID uint64) (Reservation, error) { return Reservation{}, nil },
		SubscribeReservationsFunc:   func(ch chan<- Reservation) (id ReservationsSubID) { return "" },
		UnsubscribeReservationsFunc: func(id ReservationsSubID) {},
		CallWatcher:                 testutil.NewCallWatcher(),
	}
}

func (r *MockReservationService) Reserve(ctx context.Context, rr ReservationRequest) (Reservation, error) {
	r.CallWatcher.AddCall(ctx, rr)
	return r.ReserveFunc(ctx, rr)
}

func (r *MockReservationService) GetReservations(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error) {
	r.CallWatcher.AddCall(ctx, options, limit, offset)
	return r.GetReservationsFunc(ctx, options, limit, offset)
}

func (r *MockReservationService) GetReservation(ctx context.Context, ID uint64) (Reservation, error) {
	r.CallWatcher.AddCall(ctx, ID)
	return r.GetReservationFunc(ctx, ID)
}

func (r *MockReservationService) SubscribeReservations(ch chan<- Reservation) (id ReservationsSubID) {
	r.CallWatcher.AddCall(ch)
	return r.SubscribeReservationsFunc(ch)
}

func (r *MockReservationService) UnsubscribeReservations(id ReservationsSubID) {
	r.CallWatcher.AddCall(id)
	r.UnsubscribeReservationsFunc(id)
}
