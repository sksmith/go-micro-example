package inventory

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/render"
)

type ProductResponse struct {
	ProductInventory

	// Catalog is the optional enrichment from the upstream catalog
	// service (DSN-018). Omitted when the catalog client is disabled
	// or when the upstream is unreachable — the inventory response
	// still succeeds in that case.
	Catalog *CatalogInfo `json:"catalog,omitempty"`
} // @name ProductResponse

// CatalogInfo is the subset of upstream catalog data the inventory
// API exposes. Keep this in sync with core/catalog.Product —
// fields added there should be threaded through here only when the
// API contract should change.
type CatalogInfo struct {
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
} // @name CatalogInfo

func NewProductResponse(product ProductInventory) *ProductResponse {
	resp := &ProductResponse{ProductInventory: product}
	return resp
}

func (rd *ProductResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	// Pre-processing before a response is marshalled and sent across the wire
	return nil
}

func NewProductListResponse(products []ProductInventory) []render.Renderer {
	list := make([]render.Renderer, 0)
	for _, product := range products {
		list = append(list, NewProductResponse(product))
	}
	return list
}

func NewReservationListResponse(reservations []Reservation) []render.Renderer {
	list := make([]render.Renderer, 0)
	for _, rsv := range reservations {
		list = append(list, &ReservationResponse{Reservation: rsv})
	}

	return list
}

type CreateProductRequest struct {
	Product
} // @name CreateProductRequest

func (p *CreateProductRequest) Bind(_ *http.Request) error {
	if p.Upc == "" || p.Name == "" || p.Sku == "" {
		return errors.New("missing required field(s)")
	}

	return nil
}

type CreateProductionEventRequest struct {
	*ProductionRequest

	ProtectedID      uint64    `json:"id"`
	ProtectedCreated time.Time `json:"created"`
} // @name CreateProductionEventRequest

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

type ProductionEventResponse struct{} // @name ProductionEventResponse

func (p *ProductionEventResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

func (p *ProductionEventResponse) Bind(_ *http.Request) error {
	return nil
}
