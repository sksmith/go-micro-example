package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
)

type InventoryService interface {
	Produce(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error
	CreateProduct(ctx context.Context, product inventory.Product) error

	GetProduct(ctx context.Context, sku string) (inventory.Product, error)
	GetAllProductInventory(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error)
	GetProductInventory(ctx context.Context, sku string) (inventory.ProductInventory, error)

	SubscribeInventory(ch chan<- inventory.ProductInventory) (id inventory.InventorySubID)
	UnsubscribeInventory(id inventory.InventorySubID)
}

type InventoryApi struct {
	service InventoryService
}

func NewInventoryApi(service InventoryService) *InventoryApi {
	return &InventoryApi{service: service}
}

const (
	CtxKeyProduct CtxKey = "product"
)

func (a *InventoryApi) ConfigureRouter(r chi.Router) {
	r.HandleFunc("/subscribe", a.Subscribe)

	r.Route("/", func(r chi.Router) {
		r.With(Paginate).Get("/", a.List)
		r.Put("/", a.CreateProduct)

		r.Route("/{sku}", func(r chi.Router) {
			r.Use(a.ProductCtx)
			r.Put("/productionEvent", a.CreateProductionEvent)
			r.Get("/", a.GetProductInventory)
		})
	})
}

// Subscribe provides consumes real-time inventory updates and sends them
// to the client via websocket connection.
//
// Note: This isn't exactly realistic because in the real world, this application
// would need to be able to scale. If it were scaled, clients would only get updates
// that occurred in their connected instance.
func (a *InventoryApi) Subscribe(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("client requesting subscription")

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Err(err).Msg("failed to establish inventory subscription connection")
		Render(w, r, ErrInternalServer)
	}
	go func() {
		defer conn.Close()

		ch := make(chan inventory.ProductInventory, 1)

		id := a.service.SubscribeInventory(ch)
		defer func() {
			a.service.UnsubscribeInventory(id)
		}()

		for inv := range ch {
			resp := &ProductResponse{ProductInventory: inv}
			body, err := json.Marshal(resp)
			if err != nil {
				log.Err(err).Interface("clientId", id).Msg("failed to marshal product response")
				continue
			}

			log.Debug().Interface("clientId", id).Interface("productResponse", resp).Msg("sending inventory update to client")
			err = wsutil.WriteServerText(conn, body)
			if err != nil {
				log.Err(err).Interface("clientId", id).Msg("failed to write server message, disconnecting client")
				return
			}
		}
	}()
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

func (a *InventoryApi) CreateProduct(w http.ResponseWriter, r *http.Request) {
	data := &CreateProductRequest{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	if err := a.service.CreateProduct(r.Context(), data.Product); err != nil {
		log.Err(err).Send()
		Render(w, r, ErrInternalServer)
		return
	}

	render.Status(r, http.StatusCreated)
	Render(w, r, NewProductResponse(inventory.ProductInventory{Product: data.Product}))
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
