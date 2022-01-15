package inventory

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
)

func NewService(repo Repository, q Queue) *service {
	return &service{repo: repo, queue: q}
}

type Service interface {
	Produce(ctx context.Context, product Product, event ProductionRequest) error
	Reserve(ctx context.Context, product Product, rr ReservationRequest) (Reservation, error)
	GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error)
	GetProduct(ctx context.Context, sku string) (Product, error)
	CreateProduct(ctx context.Context, product Product) error
	GetProductInventory(ctx context.Context, sku string) (ProductInventory, error)
	GetReservations(ctx context.Context, sku string, state ReserveState, limit, offset int) ([]Reservation, error)
}

type service struct {
	repo  Repository
	queue Queue
}

func (s *service) CreateProduct(ctx context.Context, product Product) error {
	const funcName = "CreateProduct"

	dbProduct, err := s.repo.GetProduct(ctx, product.Sku)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return errors.WithStack(err)
	}

	if dbProduct.Sku != "" {
		log.Debug().
			Str("func", funcName).
			Str("sku", dbProduct.Sku).
			Msg("product already exists")
		return nil
	}

	tx, err := s.repo.BeginTransaction(ctx)
	if err != nil {
		return errors.WithStack(err)
	}

	log.Info().
		Str("func", funcName).
		Str("sku", product.Sku).
		Str("upc", product.Upc).
		Msg("creating product")

	if err = s.repo.SaveProduct(ctx, product, core.UpdateOptions{Tx: tx}); err != nil {
		rollback(ctx, tx, err)
		return errors.WithStack(err)
	}

	log.Info().
		Str("func", funcName).
		Str("sku", product.Sku).
		Msg("creating product inventory")

	pi := ProductInventory{
		Product:   product,
		Available: 0,
	}

	if err = s.repo.SaveProductInventory(ctx, pi, core.UpdateOptions{Tx: tx}); err != nil {
		rollback(ctx, tx, err)
		return errors.WithStack(err)
	}

	if err = tx.Commit(ctx); err != nil {
		rollback(ctx, tx, err)
		return errors.WithStack(err)
	}

	return nil
}

func (s *service) Produce(ctx context.Context, product Product, pr ProductionRequest) error {
	const funcName = "Produce"

	log.Info().
		Str("func", funcName).
		Str("sku", product.Sku).
		Str("requestId", pr.RequestID).
		Int64("quantity", pr.Quantity).
		Msg("producing inventory")

	if pr.RequestID == "" {
		return errors.New("request id is required")
	}
	if pr.Quantity < 1 {
		return errors.New("quantity must be greater than zero")
	}

	event, err := s.repo.GetProductionEventByRequestID(ctx, pr.RequestID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return errors.WithStack(err)
	}

	if event.RequestID != "" {
		log.Debug().
			Str("func", funcName).
			Str("requestId", pr.RequestID).
			Msg("production request already exists")
		return nil
	}

	event = ProductionEvent{
		RequestID: pr.RequestID,
		Sku:       product.Sku,
		Quantity:  pr.Quantity,
		Created:   time.Now(),
	}

	tx, err := s.repo.BeginTransaction(ctx)
	if err != nil {
		return errors.WithStack(err)
	}
	if err = s.repo.SaveProductionEvent(ctx, &event, core.UpdateOptions{Tx: tx}); err != nil {
		rollback(ctx, tx, err)
		return errors.WithMessage(err, "failed to save production event")
	}

	productInventory, err := s.repo.GetProductInventory(ctx, product.Sku, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		rollback(ctx, tx, err)
		return errors.WithMessage(err, "failed to get product inventory")
	}

	productInventory.Available += event.Quantity
	if err = s.repo.SaveProductInventory(ctx, productInventory, core.UpdateOptions{Tx: tx}); err != nil {
		rollback(ctx, tx, err)
		return errors.WithMessage(err, "failed to add production to product")
	}

	if err = tx.Commit(ctx); err != nil {
		rollback(ctx, tx, err)
		return errors.WithMessage(err, "failed to commit production transaction")
	}

	err = s.queue.PublishInventory(ctx, productInventory)
	if err != nil {
		return errors.WithMessage(err, "failed to publish inventory")
	}

	if err = s.fillReserves(ctx, product); err != nil {
		return errors.WithMessage(err, "failed to fill reserves after production")
	}

	return nil
}

func (s *service) Reserve(ctx context.Context, pr Product, rr ReservationRequest) (Reservation, error) {
	const funcName = "Reserve"

	log.Info().
		Str("func", funcName).
		Str("requestId", rr.RequestID).
		Str("sku", pr.Sku).
		Str("requester", rr.Requester).
		Int64("quantity", rr.Quantity).
		Msg("reserving inventory")

	res, err := s.repo.GetReservationByRequestID(ctx, rr.RequestID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return Reservation{}, err
	}
	if res.RequestID != "" {
		log.Debug().Str("func", funcName).Str("requestId", rr.RequestID).Msg("reservation already exists, returning it")
		return res, nil
	}

	res = Reservation{
		RequestID:         rr.RequestID,
		Requester:         rr.Requester,
		Sku:               pr.Sku,
		State:             Open,
		ReservedQuantity:  0,
		RequestedQuantity: rr.Quantity,
		Created:           time.Now(),
	}

	tx, err := s.repo.BeginTransaction(ctx)
	if err != nil {
		return Reservation{}, errors.WithStack(err)
	}

	if err = s.repo.SaveReservation(ctx, &res, core.UpdateOptions{Tx: tx}); err != nil {
		rollback(ctx, tx, err)
		return Reservation{}, errors.WithStack(err)
	}

	if err = tx.Commit(ctx); err != nil {
		rollback(ctx, tx, err)
		return Reservation{}, errors.WithStack(err)
	}

	if err = s.fillReserves(ctx, pr); err != nil {
		return Reservation{}, errors.WithStack(err)
	}

	return res, nil
}

func (s *service) fillReserves(ctx context.Context, product Product) error {
	const funcName = "fillReserves"

	tx, err := s.repo.BeginTransaction(ctx)
	if err != nil {
		return errors.WithStack(err)
	}

	openReservations, err := s.repo.GetSkuReservationsByState(ctx, product.Sku, Open, 100, 0, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		return errors.WithStack(err)
	}

	productInventory, err := s.repo.GetProductInventory(ctx, product.Sku, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		return errors.WithStack(err)
	}

	for _, reservation := range openReservations {
		reservation := reservation

		log.Trace().
			Str("func", funcName).
			Str("sku", product.Sku).
			Str("reservation.RequestID", reservation.RequestID).
			Int64("productInventory.Available", productInventory.Available).
			Msg("fulfilling reservation")

		if productInventory.Available == 0 {
			break
		}

		fillReserve(&reservation, &productInventory)

		log.Debug().
			Str("func", funcName).
			Str("sku", product.Sku).
			Str("reservation.RequestID", reservation.RequestID).
			Msg("saving product inventory")

		err = s.repo.SaveProductInventory(ctx, productInventory, core.UpdateOptions{Tx: tx})
		if err != nil {
			rollback(ctx, tx, err)
			return errors.WithStack(err)
		}

		log.Debug().
			Str("func", funcName).
			Str("sku", product.Sku).
			Str("reservation.RequestID", reservation.RequestID).
			Str("state", string(reservation.State)).
			Msg("updating reservation")

		err = s.repo.UpdateReservation(ctx, reservation.ID, reservation.State, reservation.ReservedQuantity, core.UpdateOptions{Tx: tx})
		if err != nil {
			rollback(ctx, tx, err)
			return errors.WithStack(err)
		}

		err = s.queue.PublishInventory(ctx, productInventory)
		if err != nil {
			rollback(ctx, tx, err)
			return errors.WithMessage(err, "failed to publish inventory")
		}

		if reservation.State == Closed {
			err := s.queue.PublishReservation(ctx, reservation)
			if err != nil {
				rollback(ctx, tx, err)
				return err
			}
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func fillReserve(reservation *Reservation, productInventory *ProductInventory) {
	reserveAmount := reservation.RequestedQuantity - reservation.ReservedQuantity
	if reserveAmount > productInventory.Available {
		reserveAmount = productInventory.Available
	}
	productInventory.Available -= reserveAmount
	reservation.ReservedQuantity += reserveAmount

	if reservation.ReservedQuantity == reservation.RequestedQuantity {
		reservation.State = Closed
	}
}

func (s *service) GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
	return s.repo.GetAllProductInventory(ctx, limit, offset)
}

func (s *service) GetProduct(ctx context.Context, sku string) (Product, error) {
	const funcName = "GetProduct"

	log.Info().
		Str("func", funcName).
		Str("sku", sku).
		Msg("getting product")

	product, err := s.repo.GetProduct(ctx, sku)
	if err != nil {
		return product, errors.WithStack(err)
	}
	return product, nil
}

func (s *service) GetProductInventory(ctx context.Context, sku string) (ProductInventory, error) {
	const funcName = "GetProductInventory"

	log.Info().
		Str("func", funcName).
		Str("sku", sku).
		Msg("getting product inventory")

	product, err := s.repo.GetProductInventory(ctx, sku)
	if err != nil {
		return product, errors.WithStack(err)
	}
	return product, nil
}

func (s *service) GetReservations(ctx context.Context, sku string, state ReserveState, limit, offset int) ([]Reservation, error) {
	const funcName = "GetProductInventory"

	log.Info().
		Str("func", funcName).
		Str("sku", sku).
		Msg("getting reservations")

	rsv, err := s.repo.GetSkuReservationsByState(ctx, sku, state, limit, offset)
	if err != nil {
		return rsv, errors.WithStack(err)
	}
	return rsv, nil
}

func rollback(ctx context.Context, tx core.Transaction, err error) {
	e := tx.Rollback(ctx)
	if e != nil {
		log.Warn().Err(err).Msg("failed to rollback")
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
	GetSkuReservationsByState(ctx context.Context, sku string, state ReserveState, limit, offset int, options ...core.QueryOptions) ([]Reservation, error)
	GetReservationByRequestID(ctx context.Context, requestId string, options ...core.QueryOptions) (Reservation, error)

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

type Queue interface {
	PublishInventory(ctx context.Context, productInventory ProductInventory) error
	PublishReservation(ctx context.Context, reservation Reservation) error
}
