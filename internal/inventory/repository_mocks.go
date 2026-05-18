package inventory

import (
	"context"

	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/db"
)

type MockRepo struct {
	GetProductionEventByRequestIDFunc func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe ProductionEvent, err error)
	SaveProductionEventFunc           func(ctx context.Context, event *ProductionEvent, options ...core.UpdateOptions) error

	GetReservationFunc            func(ctx context.Context, ID uint64, options ...core.QueryOptions) (Reservation, error)
	GetReservationsFunc           func(ctx context.Context, resOptions GetReservationsOptions, limit, offset int, options ...core.QueryOptions) ([]Reservation, error)
	GetReservationByRequestIDFunc func(ctx context.Context, requestId string, options ...core.QueryOptions) (Reservation, error)
	UpdateReservationFunc         func(ctx context.Context, ID uint64, state ReserveState, qty int64, options ...core.UpdateOptions) error
	SaveReservationFunc           func(ctx context.Context, reservation *Reservation, options ...core.UpdateOptions) error

	GetProductFunc  func(ctx context.Context, sku string, options ...core.QueryOptions) (Product, error)
	SaveProductFunc func(ctx context.Context, product Product, options ...core.UpdateOptions) error

	GetProductInventoryFunc    func(ctx context.Context, sku string, options ...core.QueryOptions) (ProductInventory, error)
	GetAllProductInventoryFunc func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]ProductInventory, error)
	SaveProductInventoryFunc   func(ctx context.Context, productInventory ProductInventory, options ...core.UpdateOptions) error

	BeginTransactionFunc func(ctx context.Context) (core.Transaction, error)

	GetProductionEventByRequestIDCalls int
	SaveProductionEventCalls           int
	GetReservationCalls                int
	GetReservationsCalls               int
	GetReservationByRequestIDCalls     int
	UpdateReservationCalls             int
	SaveReservationCalls               int
	GetProductCalls                    int
	SaveProductCalls                   int
	GetProductInventoryCalls           int
	GetAllProductInventoryCalls        int
	SaveProductInventoryCalls          int
	BeginTransactionCalls              int
}

func (r *MockRepo) SaveProductionEvent(ctx context.Context, event *ProductionEvent, options ...core.UpdateOptions) error {
	r.SaveProductionEventCalls++
	return r.SaveProductionEventFunc(ctx, event, options...)
}

func (r *MockRepo) UpdateReservation(ctx context.Context, ID uint64, state ReserveState, qty int64, options ...core.UpdateOptions) error {
	r.UpdateReservationCalls++
	return r.UpdateReservationFunc(ctx, ID, state, qty, options...)
}

func (r *MockRepo) GetProductionEventByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (pe ProductionEvent, err error) {
	r.GetProductionEventByRequestIDCalls++
	return r.GetProductionEventByRequestIDFunc(ctx, requestID, options...)
}

func (r *MockRepo) SaveReservation(ctx context.Context, reservation *Reservation, options ...core.UpdateOptions) error {
	r.SaveReservationCalls++
	return r.SaveReservationFunc(ctx, reservation, options...)
}

func (r *MockRepo) GetReservation(ctx context.Context, ID uint64, options ...core.QueryOptions) (Reservation, error) {
	r.GetReservationCalls++
	return r.GetReservationFunc(ctx, ID, options...)
}

func (r *MockRepo) GetReservations(ctx context.Context, resOptions GetReservationsOptions, limit, offset int, options ...core.QueryOptions) ([]Reservation, error) {
	r.GetReservationsCalls++
	return r.GetReservationsFunc(ctx, resOptions, limit, offset, options...)
}

func (r *MockRepo) SaveProduct(ctx context.Context, product Product, options ...core.UpdateOptions) error {
	r.SaveProductCalls++
	return r.SaveProductFunc(ctx, product, options...)
}

func (r *MockRepo) GetProduct(ctx context.Context, sku string, options ...core.QueryOptions) (Product, error) {
	r.GetProductCalls++
	return r.GetProductFunc(ctx, sku, options...)
}

func (r *MockRepo) GetProductInventory(ctx context.Context, sku string, options ...core.QueryOptions) (ProductInventory, error) {
	r.GetProductInventoryCalls++
	return r.GetProductInventoryFunc(ctx, sku, options...)
}

func (r *MockRepo) SaveProductInventory(ctx context.Context, productInventory ProductInventory, options ...core.UpdateOptions) error {
	r.SaveProductInventoryCalls++
	return r.SaveProductInventoryFunc(ctx, productInventory, options...)
}

func (r *MockRepo) GetAllProductInventory(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]ProductInventory, error) {
	r.GetAllProductInventoryCalls++
	return r.GetAllProductInventoryFunc(ctx, limit, offset, options...)
}

func (r *MockRepo) BeginTransaction(ctx context.Context) (core.Transaction, error) {
	r.BeginTransactionCalls++
	return r.BeginTransactionFunc(ctx)
}

func (r *MockRepo) GetReservationByRequestID(ctx context.Context, requestId string, options ...core.QueryOptions) (Reservation, error) {
	r.GetReservationByRequestIDCalls++
	return r.GetReservationByRequestIDFunc(ctx, requestId, options...)
}

func NewMockRepo() *MockRepo {
	return &MockRepo{
		SaveProductionEventFunc: func(ctx context.Context, event *ProductionEvent, options ...core.UpdateOptions) error {
			return nil
		},
		GetProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe ProductionEvent, err error) {
			return ProductionEvent{}, nil
		},
		SaveReservationFunc: func(ctx context.Context, reservation *Reservation, options ...core.UpdateOptions) error {
			return nil
		},
		GetReservationFunc: func(ctx context.Context, ID uint64, options ...core.QueryOptions) (Reservation, error) {
			return Reservation{}, nil
		},
		GetReservationsFunc: func(ctx context.Context, resOptions GetReservationsOptions, limit, offset int, options ...core.QueryOptions) ([]Reservation, error) {
			return nil, nil
		},
		SaveProductFunc: func(ctx context.Context, product Product, options ...core.UpdateOptions) error { return nil },
		GetProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (Product, error) {
			return Product{}, nil
		},
		GetAllProductInventoryFunc: func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]ProductInventory, error) {
			return nil, nil
		},
		BeginTransactionFunc: func(ctx context.Context) (core.Transaction, error) { return db.NewMockTransaction(), nil },
		GetReservationByRequestIDFunc: func(ctx context.Context, requestId string, options ...core.QueryOptions) (Reservation, error) {
			return Reservation{}, nil
		},
		UpdateReservationFunc: func(ctx context.Context, ID uint64, state ReserveState, qty int64, options ...core.UpdateOptions) error {
			return nil
		},
		GetProductInventoryFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (ProductInventory, error) {
			return ProductInventory{}, nil
		},
		SaveProductInventoryFunc: func(ctx context.Context, productInventory ProductInventory, options ...core.UpdateOptions) error {
			return nil
		},
	}
}
