package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/render"
	"github.com/sksmith/go-micro-example/core/inventory"
)

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
		list = append(list, &ReservationResponse{Reservation: rsv})
	}

	return list
}

type CreateProductRequest struct {
	inventory.Product
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
