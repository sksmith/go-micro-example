package inventory

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
)

func NewService(repo Repository, q InventoryQueue) *service {
	log.Info().Msg("creating inventory service...")
	return &service{
		repo:            repo,
		queue:           q,
		inventorySubs:   make(map[InventorySubID]chan<- ProductInventory),
		reservationSubs: make(map[ReservationsSubID]chan<- Reservation),
	}
}

type InventorySubID string
type ReservationsSubID string

type GetReservationsOptions struct {
	Sku   string
	State ReserveState
}

type service struct {
	repo            Repository
	queue           InventoryQueue
	inventorySubs   map[InventorySubID]chan<- ProductInventory
	reservationSubs map[ReservationsSubID]chan<- Reservation
}

func (s *service) CreateProduct(ctx context.Context, product Product) error {
	const funcName = "CreateProduct"

	dbProduct, err := s.repo.GetProduct(ctx, product.Sku)
	if err != nil != errors.Is(err, core.ErrNotFound) {
		return errors.WithStack(err)
	}
	if err == nil {
		log.Debug().Str("func", funcName).Str("sku", dbProduct.Sku).Msg("product already exists")
		return nil
	}

	tx, err := s.repo.BeginTransaction(ctx)
	if err != nil {
		return errors.WithStack(err)
	}
	defer func() {
		if err != nil {
			rollback(ctx, tx, err)
		}
	}()

	log.Debug().Str("func", funcName).Str("sku", product.Sku).Msg("creating product")
	if err = s.repo.SaveProduct(ctx, product, core.UpdateOptions{Tx: tx}); err != nil {
		return errors.WithStack(err)
	}

	log.Debug().Str("func", funcName).Str("sku", product.Sku).Msg("creating product inventory")
	pi := ProductInventory{Product: product}

	if err = s.repo.SaveProductInventory(ctx, pi, core.UpdateOptions{Tx: tx}); err != nil {
		return errors.WithStack(err)
	}

	if err = tx.Commit(ctx); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (s *service) Produce(ctx context.Context, product Product, pr ProductionRequest) error {
	const funcName = "Produce"

	log.Debug().
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
		log.Debug().Str("func", funcName).Str("requestId", pr.RequestID).Msg("production request already exists")
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
	defer func() {
		if err != nil {
			rollback(ctx, tx, err)
		}
	}()

	if err = s.repo.SaveProductionEvent(ctx, &event, core.UpdateOptions{Tx: tx}); err != nil {
		return errors.WithMessage(err, "failed to save production event")
	}

	productInventory, err := s.repo.GetProductInventory(ctx, product.Sku, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		return errors.WithMessage(err, "failed to get product inventory")
	}

	productInventory.Available += event.Quantity
	if err = s.repo.SaveProductInventory(ctx, productInventory, core.UpdateOptions{Tx: tx}); err != nil {
		return errors.WithMessage(err, "failed to add production to product")
	}

	if err = tx.Commit(ctx); err != nil {
		return errors.WithMessage(err, "failed to commit production transaction")
	}

	err = s.publishInventory(ctx, productInventory)
	if err != nil {
		return errors.WithMessage(err, "failed to publish inventory")
	}

	if err = s.FillReserves(ctx, product); err != nil {
		return errors.WithMessage(err, "failed to fill reserves after production")
	}

	return nil
}

func (s *service) Reserve(ctx context.Context, rr ReservationRequest) (Reservation, error) {
	const funcName = "Reserve"

	log.Debug().
		Str("func", funcName).
		Str("requestID", rr.RequestID).
		Str("sku", rr.Sku).
		Str("requester", rr.Requester).
		Int64("quantity", rr.Quantity).
		Msg("reserving inventory")

	if err := validateReservationRequest(rr); err != nil {
		return Reservation{}, err
	}

	tx, err := s.repo.BeginTransaction(ctx)
	defer func() {
		if err != nil {
			rollback(ctx, tx, err)
		}
	}()
	if err != nil {
		return Reservation{}, err
	}

	pr, err := s.repo.GetProduct(ctx, rr.Sku, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		log.Error().Err(err).Str("requestId", rr.RequestID).Msg("failed to get product")
		return Reservation{}, errors.WithStack(err)
	}

	res, err := s.repo.GetReservationByRequestID(ctx, rr.RequestID, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		log.Error().Err(err).Str("requestId", rr.RequestID).Msg("failed to get reservation request")
		return Reservation{}, errors.WithStack(err)
	}
	if res.RequestID != "" {
		log.Debug().Str("func", funcName).Str("requestId", rr.RequestID).Msg("reservation already exists, returning it")
		rollback(ctx, tx, err)
		return res, nil
	}

	res = Reservation{
		RequestID:         rr.RequestID,
		Requester:         rr.Requester,
		Sku:               rr.Sku,
		State:             Open,
		RequestedQuantity: rr.Quantity,
		Created:           time.Now(),
	}

	if err = s.repo.SaveReservation(ctx, &res, core.UpdateOptions{Tx: tx}); err != nil {
		return Reservation{}, errors.WithStack(err)
	}

	if err = tx.Commit(ctx); err != nil {
		return Reservation{}, errors.WithStack(err)
	}

	if err = s.FillReserves(ctx, pr); err != nil {
		return Reservation{}, errors.WithStack(err)
	}

	return res, nil
}

func validateReservationRequest(rr ReservationRequest) error {
	if rr.RequestID == "" {
		return errors.New("request id is required")
	}
	if rr.Requester == "" {
		return errors.New("requester is required")
	}
	if rr.Sku == "" {
		return errors.New("sku is requred")
	}
	if rr.Quantity < 1 {
		return errors.New("quantity is required")
	}
	return nil
}

func (s *service) GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error) {
	return s.repo.GetAllProductInventory(ctx, limit, offset)
}

func (s *service) GetProduct(ctx context.Context, sku string) (Product, error) {
	const funcName = "GetProduct"

	log.Debug().Str("func", funcName).Str("sku", sku).Msg("getting product")

	product, err := s.repo.GetProduct(ctx, sku)
	if err != nil {
		return product, errors.WithStack(err)
	}
	return product, nil
}

func (s *service) GetProductInventory(ctx context.Context, sku string) (ProductInventory, error) {
	const funcName = "GetProductInventory"

	log.Debug().Str("func", funcName).Str("sku", sku).Msg("getting product inventory")

	product, err := s.repo.GetProductInventory(ctx, sku)
	if err != nil {
		return product, errors.WithStack(err)
	}
	return product, nil
}

func (s *service) GetReservation(ctx context.Context, ID uint64) (Reservation, error) {
	const funcName = "GetReservation"

	log.Debug().Str("func", funcName).Uint64("id", ID).Msg("getting reservation")

	rsv, err := s.repo.GetReservation(ctx, ID)
	if err != nil {
		return rsv, errors.WithStack(err)
	}
	return rsv, nil
}

func (s *service) GetReservations(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error) {
	const funcName = "GetProductInventory"

	log.Debug().
		Str("func", funcName).
		Str("sku", options.Sku).
		Str("state", string(options.State)).
		Msg("getting reservations")

	rsv, err := s.repo.GetReservations(ctx, options, limit, offset)
	if err != nil {
		return rsv, errors.WithStack(err)
	}
	return rsv, nil
}

func (s *service) SubscribeInventory(ch chan<- ProductInventory) (id InventorySubID) {
	id = InventorySubID(uuid.NewString())
	s.inventorySubs[id] = ch
	log.Debug().Interface("clientId", id).Msg("subscribing to inventory")
	return id
}

func (s *service) UnsubscribeInventory(id InventorySubID) {
	log.Debug().Interface("clientId", id).Msg("unsubscribing from inventory")
	close(s.inventorySubs[id])
	delete(s.inventorySubs, id)
}

func (s *service) SubscribeReservations(ch chan<- Reservation) (id ReservationsSubID) {
	id = ReservationsSubID(uuid.NewString())
	s.reservationSubs[id] = ch
	log.Debug().Interface("clientId", id).Msg("subscribing to reservations")
	return id
}

func (s *service) UnsubscribeReservations(id ReservationsSubID) {
	log.Debug().Interface("clientId", id).Msg("unsubscribing from reservations")
	close(s.reservationSubs[id])
	delete(s.reservationSubs, id)
}

func (s *service) FillReserves(ctx context.Context, product Product) error {
	const funcName = "fillReserves"

	tx, err := s.repo.BeginTransaction(ctx)
	defer func() {
		if err != nil {
			rollback(ctx, tx, err)
		}
	}()
	if err != nil {
		return errors.WithStack(err)
	}

	openReservations, err := s.repo.GetReservations(ctx, GetReservationsOptions{Sku: product.Sku, State: Open}, 100, 0, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		return errors.WithStack(err)
	}

	productInventory, err := s.repo.GetProductInventory(ctx, product.Sku, core.QueryOptions{Tx: tx, ForUpdate: true})
	if err != nil {
		return errors.WithStack(err)
	}

	for _, reservation := range openReservations {
		var subtx pgx.Tx
		subtx, err = tx.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				rollback(ctx, subtx, err)
			}
		}()
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

		reserveAmount := reservation.RequestedQuantity - reservation.ReservedQuantity
		if reserveAmount > productInventory.Available {
			reserveAmount = productInventory.Available
		}
		productInventory.Available -= reserveAmount
		reservation.ReservedQuantity += reserveAmount

		if reservation.ReservedQuantity == reservation.RequestedQuantity {
			reservation.State = Closed
		}

		log.Debug().
			Str("func", funcName).
			Str("sku", product.Sku).
			Str("reservation.RequestID", reservation.RequestID).
			Msg("saving product inventory")

		err = s.repo.SaveProductInventory(ctx, productInventory, core.UpdateOptions{Tx: tx})
		if err != nil {
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
			return errors.WithStack(err)
		}

		if err = subtx.Commit(ctx); err != nil {
			return errors.WithStack(err)
		}

		err = s.publishInventory(ctx, productInventory)
		if err != nil {
			return errors.WithStack(err)
		}

		err = s.publishReservation(ctx, reservation)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (s *service) publishInventory(ctx context.Context, pi ProductInventory) error {
	err := s.queue.PublishInventory(ctx, pi)
	if err != nil {
		return errors.WithMessage(err, "failed to publish inventory to queue")
	}
	go s.notifyInventorySubscribers(pi)
	return nil
}

func (s *service) publishReservation(ctx context.Context, r Reservation) error {
	err := s.queue.PublishReservation(ctx, r)
	if err != nil {
		return errors.WithMessage(err, "failed to publish reservation to queue")
	}
	go s.notifyReservationSubscribers(r)
	return nil
}

func (s *service) notifyInventorySubscribers(pi ProductInventory) {
	for id, ch := range s.inventorySubs {
		log.Debug().Interface("clientId", id).Interface("productInventory", pi).Msg("notifying subscriber of inventory update")
		ch <- pi
	}
}

func (s *service) notifyReservationSubscribers(r Reservation) {
	for id, ch := range s.reservationSubs {
		log.Debug().Interface("clientId", id).Interface("productInventory", r).Msg("notifying subscriber of reservation update")
		ch <- r
	}
}
