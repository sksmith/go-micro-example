package invrepo

import (
	"context"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
)

type MockRepo struct {
	GetProductionEventByRequestIDFunc func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error)
	SaveProductionEventFunc           func(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error

	GetSkuReservesByStateFunc     func(ctx context.Context, sku string, state inventory.ReserveState, limit, offset int, options ...core.QueryOptions) ([]inventory.Reservation, error)
	GetReservationByRequestIDFunc func(ctx context.Context, requestId string, options ...core.QueryOptions) (inventory.Reservation, error)
	UpdateReservationFunc         func(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...core.UpdateOptions) error
	SaveReservationFunc           func(ctx context.Context, reservation *inventory.Reservation, options ...core.UpdateOptions) error

	GetProductFunc  func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error)
	SaveProductFunc func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error

	GetProductInventoryFunc    func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error)
	GetAllProductInventoryFunc func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error)
	SaveProductInventoryFunc   func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error

	BeginTransactionFunc func(ctx context.Context) (core.Transaction, error)
}

func (r MockRepo) SaveProductionEvent(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error {
	return r.SaveProductionEventFunc(ctx, event, options...)
}

func (r MockRepo) UpdateReservation(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...core.UpdateOptions) error {
	return r.UpdateReservationFunc(ctx, ID, state, qty, options...)
}

func (r MockRepo) GetProductionEventByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
	return r.GetProductionEventByRequestIDFunc(ctx, requestID, options...)
}

func (r MockRepo) SaveReservation(ctx context.Context, reservation *inventory.Reservation, options ...core.UpdateOptions) error {
	return r.SaveReservationFunc(ctx, reservation, options...)
}

func (r MockRepo) GetSkuReservationsByState(ctx context.Context, sku string, state inventory.ReserveState, limit, offset int, options ...core.QueryOptions) ([]inventory.Reservation, error) {
	return r.GetSkuReservesByStateFunc(ctx, sku, state, limit, offset, options...)
}

func (r MockRepo) SaveProduct(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
	return r.SaveProductFunc(ctx, product, options...)
}

func (r MockRepo) GetProduct(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
	return r.GetProductFunc(ctx, sku, options...)
}

func (r MockRepo) GetProductInventory(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error) {
	return r.GetProductInventoryFunc(ctx, sku, options...)
}

func (r MockRepo) SaveProductInventory(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
	return r.SaveProductInventoryFunc(ctx, productInventory, options...)
}

func (r MockRepo) GetAllProductInventory(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
	return r.GetAllProductInventoryFunc(ctx, limit, offset, options...)
}

func (r MockRepo) BeginTransaction(ctx context.Context) (core.Transaction, error) {
	return r.BeginTransactionFunc(ctx)
}

func (r MockRepo) GetReservationByRequestID(ctx context.Context, requestId string, options ...core.QueryOptions) (inventory.Reservation, error) {
	return r.GetReservationByRequestIDFunc(ctx, requestId, options...)
}

func NewMockRepo() MockRepo {
	return MockRepo{
		SaveProductionEventFunc: func(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error {
			return nil
		},
		GetProductionEventByRequestIDFunc: func(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
			return inventory.ProductionEvent{}, nil
		},
		SaveReservationFunc: func(ctx context.Context, reservation *inventory.Reservation, options ...core.UpdateOptions) error {
			return nil
		},
		GetSkuReservesByStateFunc: func(ctx context.Context, sku string, state inventory.ReserveState, limit, offset int, options ...core.QueryOptions) ([]inventory.Reservation, error) {
			return nil, nil
		},
		SaveProductFunc: func(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error { return nil },
		GetProductFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
			return inventory.Product{}, nil
		},
		GetAllProductInventoryFunc: func(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
			return nil, nil
		},
		BeginTransactionFunc: func(ctx context.Context) (core.Transaction, error) { return MockTransaction{}, nil },
		GetReservationByRequestIDFunc: func(ctx context.Context, requestId string, options ...core.QueryOptions) (inventory.Reservation, error) {
			return inventory.Reservation{}, nil
		},
		GetProductInventoryFunc: func(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error) {
			return inventory.ProductInventory{}, nil
		},
		SaveProductInventoryFunc: func(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
			return nil
		},
	}
}

type MockTransaction struct {
}

func (m MockTransaction) Commit(_ context.Context) error {
	return nil
}

func (m MockTransaction) Rollback(_ context.Context) error {
	return nil
}

func (m MockTransaction) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (m MockTransaction) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	return nil
}

func (m MockTransaction) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return nil, nil
}

func (m MockTransaction) Begin(_ context.Context) (pgx.Tx, error) {
	return nil, nil
}

func (m MockTransaction) Conn() core.Conn {
	return nil
}
