package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
)

type InventoryApi struct {
	service inventory.Service
}

func NewInventoryApi(service inventory.Service) *InventoryApi {
	return &InventoryApi{service: service}
}

const (
	CtxKeyProduct     CtxKey = "product"
	CtxKeyReservation CtxKey = "reservation"
)

func (a *InventoryApi) ConfigureRouter(r chi.Router) {
	r.Route("/v1", func(r chi.Router) {
		r.With(Paginate).Get("/", a.List)
		r.Put("/", a.Create)

		r.Route("/{sku}", func(r chi.Router) {
			r.Use(a.ProductCtx)
			r.Put("/productionEvent", a.CreateProductionEvent)
			r.Get("/", a.GetProductInventory)

			r.Route("/reservation", func(r chi.Router) {
				r.Put("/", a.CreateReservation)
				r.With(Paginate).Get("/", a.GetReservations)

				r.Route("/{reservationID}", func(r chi.Router) {
					r.Use(a.ReservationCtx)
					r.Delete("/", a.CancelReservation)
				})
			})
		})
	})
}

type ProductResponse struct {
	inventory.ProductInventory
}

func NewProductResponse(product inventory.ProductInventory) *ProductResponse {
	resp := &ProductResponse{ProductInventory: product}
	return resp
}

func (rd *ProductResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	// Pre-processing before a response is marshalled and sent across the wire
	return nil
}

func (a *InventoryApi) List(w http.ResponseWriter, r *http.Request) {
	limit := r.Context().Value(CtxKeyLimit).(int)
	offset := r.Context().Value(CtxKeyOffset).(int)

	products, err := a.service.GetAllProductInventory(r.Context(), limit, offset)
	if err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}

	RenderList(w, r, NewProductListResponse(products))
}

func (a *InventoryApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &CreateProductRequest{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	if err := a.service.CreateProduct(r.Context(), *data.Product); err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}

	render.Status(r, http.StatusCreated)
	Render(w, r, NewProductResponse(inventory.ProductInventory{Product: *data.Product}))
}

func (a *InventoryApi) ProductCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var product inventory.Product
		var err error

		sku := chi.URLParam(r, "sku")
		if sku == "" {
			Render(w, r, ErrInvalidRequest(errors.New("sku is required")))
			return
		}

		product, err = a.service.GetProduct(r.Context(), sku)

		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				Render(w, r, ErrNotFound)
			} else {
				log.Error().Err(err).Str("sku", sku).Msg("error acquiring product")
				Render(w, r, ErrInternalServer)
			}
			return
		}

		ctx := context.WithValue(r.Context(), CtxKeyProduct, product)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func NewProductListResponse(products []inventory.ProductInventory) []render.Renderer {
	list := make([]render.Renderer, 0)
	for _, product := range products {
		list = append(list, NewProductResponse(product))
	}
	return list
}

func NewReservationListResponse(reservations []inventory.Reservation) []render.Renderer {
	list := make([]render.Renderer, 0)
	for _, rsv := range reservations {
		log.Info().Interface("appending", rsv).Send()
		list = append(list, &ReservationResponse{Reservation: rsv})
	}

	return list
}

type CreateProductRequest struct {
	*inventory.Product

	// we don't want to allow setting quantities upon creation of a product
	ProtectedReserved  int `json:"reserved"`
	ProtectedAvailable int `json:"available"`
}

func (p *CreateProductRequest) Bind(_ *http.Request) error {
	if p.Upc == "" || p.Name == "" || p.Sku == "" {
		return errors.New("missing required field(s)")
	}

	return nil
}

type CreateProductionEventRequest struct {
	*inventory.ProductionRequest

	ProtectedID      uint64    `json:"id"`
	ProtectedCreated time.Time `json:"created"`
}

func (p *CreateProductionEventRequest) Bind(_ *http.Request) error {
	if p.ProductionRequest == nil {
		return errors.New("missing required ProductionRequest fields")
	}
	if p.RequestID == "" {
		return errors.New("requestId is required")
	}
	if p.Quantity < 1 {
		return errors.New("quantity must be greater than zero")
	}

	return nil
}

type ProductionEventResponse struct {
}

func (p *ProductionEventResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

func (p *ProductionEventResponse) Bind(_ *http.Request) error {
	return nil
}

type ReservationRequest struct {
	*inventory.ReservationRequest
}

func (r *ReservationRequest) Bind(_ *http.Request) error {
	if r.ReservationRequest == nil {
		return errors.New("missing required Reservation fields")
	}
	if r.Requester == "" {
		return errors.New("requester is required")
	}
	if r.Quantity < 1 {
		return errors.New("requested quantity must be greater than zero")
	}

	return nil
}

type ReservationResponse struct {
	inventory.Reservation
}

func (r *ReservationResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

func (a *InventoryApi) CreateProductionEvent(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(inventory.Product)

	data := &CreateProductionEventRequest{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	if err := a.service.Produce(r.Context(), product, *data.ProductionRequest); err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}

	render.Status(r, http.StatusCreated)
	Render(w, r, &ProductionEventResponse{})
}

func (a *InventoryApi) CancelReservation(_ http.ResponseWriter, _ *http.Request) {
	// Not implemented
}

func (a *InventoryApi) ReservationCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Not implemented
		ctx := context.WithValue(r.Context(), CtxKeyReservation, nil)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *InventoryApi) CreateReservation(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(inventory.Product)

	data := &ReservationRequest{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	res, err := a.service.Reserve(r.Context(), product, *data.ReservationRequest)
	if err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}

	resp := &ReservationResponse{Reservation: res}
	render.Status(r, http.StatusCreated)
	Render(w, r, resp)
}

func (a *InventoryApi) GetProductInventory(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(inventory.Product)

	res, err := a.service.GetProductInventory(r.Context(), product.Sku)

	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			Render(w, r, ErrNotFound)
		} else {
			log.Err(err).Send()
			Render(w, r, ErrInternalServer)
		}
		return
	}

	resp := &ProductResponse{ProductInventory: res}
	render.Status(r, http.StatusOK)
	Render(w, r, resp)
}

func (a *InventoryApi) GetReservations(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(inventory.Product)
	limit := r.Context().Value(CtxKeyLimit).(int)
	offset := r.Context().Value(CtxKeyOffset).(int)

	state, err := inventory.ParseReserveState(r.URL.Query().Get("state"))
	if err != nil {
		Render(w, r, ErrInvalidRequest(errors.New("invalid state")))
		return
	}

	res, err := a.service.GetReservations(r.Context(), product.Sku, state, limit, offset)

	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			Render(w, r, ErrNotFound)
		} else {
			log.Err(err).Send()
			Render(w, r, ErrInternalServer)
		}
		return
	}

	resList := NewReservationListResponse(res)
	render.Status(r, http.StatusOK)
	RenderList(w, r, resList)
}
