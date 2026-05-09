package inventory

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
)

// rollback issues a Rollback on tx (no-op if tx is nil) and, on
// failure, logs the rollback error alongside the triggering error
// so the cause of the abort and the cause of the failed cleanup are
// both visible. Earlier versions logged the trigger error under a
// "failed to rollback" message, which silently hid actual rollback
// failures (e.g. dead connection).
func rollback(ctx context.Context, tx core.Transaction, trigger error) {
	if tx == nil {
		return
	}
	if rbErr := tx.Rollback(ctx); rbErr != nil {
		log.Ctx(ctx).Warn().Err(rbErr).AnErr("trigger", trigger).Msg("failed to rollback")
	}
}

type Transactional interface {
	BeginTransaction(ctx context.Context) (core.Transaction, error)
}

type Repository interface {
	ProductionEventRepository
	ReservationRepository
	InventoryRepository
	ProductRepository
}

type ProductionEventRepository interface {
	Transactional
	GetProductionEventByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (pe ProductionEvent, err error)

	SaveProductionEvent(ctx context.Context, event *ProductionEvent, options ...core.UpdateOptions) error
}

type ReservationRepository interface {
	Transactional
	GetReservations(ctx context.Context, resOptions GetReservationsOptions, limit, offset int, options ...core.QueryOptions) ([]Reservation, error)
	GetReservationByRequestID(ctx context.Context, requestId string, options ...core.QueryOptions) (Reservation, error)
	GetReservation(ctx context.Context, ID uint64, options ...core.QueryOptions) (Reservation, error)

	SaveReservation(ctx context.Context, reservation *Reservation, options ...core.UpdateOptions) error
	UpdateReservation(ctx context.Context, ID uint64, state ReserveState, qty int64, options ...core.UpdateOptions) error
}

type InventoryRepository interface {
	Transactional
	GetProductInventory(ctx context.Context, sku string, options ...core.QueryOptions) (pi ProductInventory, err error)
	GetAllProductInventory(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]ProductInventory, error)

	SaveProductInventory(ctx context.Context, productInventory ProductInventory, options ...core.UpdateOptions) error
}

type ProductRepository interface {
	Transactional
	GetProduct(ctx context.Context, sku string, options ...core.QueryOptions) (Product, error)

	SaveProduct(ctx context.Context, product Product, options ...core.UpdateOptions) error
}

type InventoryQueue interface {
	PublishInventory(ctx context.Context, productInventory ProductInventory) error
	PublishReservation(ctx context.Context, reservation Reservation) error
}
