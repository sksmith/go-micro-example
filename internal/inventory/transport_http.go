package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/catalog"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

// CtxKey is the context-key type used by handlers in this package to
// stash request-scoped values (e.g. resolved product / reservation
// objects from URL params) for downstream middleware and handlers.
type CtxKey string

type InventoryService interface {
	Produce(ctx context.Context, product Product, event ProductionRequest) error
	CreateProduct(ctx context.Context, product Product) error

	GetProduct(ctx context.Context, sku string) (Product, error)
	GetAllProductInventory(ctx context.Context, limit, offset int) ([]ProductInventory, error)
	GetProductInventory(ctx context.Context, sku string) (ProductInventory, error)

	SubscribeInventory(ch chan<- ProductInventory) (id InventorySubID)
	UnsubscribeInventory(id InventorySubID)
}

type InventoryApi struct {
	service     InventoryService
	catalog     catalog.Client
	idempotency func(http.Handler) http.Handler
}

func NewInventoryApi(service InventoryService) *InventoryApi {
	return &InventoryApi{service: service}
}

// SetIdempotency installs the optional Idempotency-Key middleware
// (DSN-019). When set, the productionEvent route requires the header
// and replays cached responses on retry. A nil argument is a no-op.
func (a *InventoryApi) SetIdempotency(mw func(http.Handler) http.Handler) {
	a.idempotency = mw
}

// SetCatalog installs the optional outbound catalog client (DSN-018).
// When set, GetProductInventory enriches its response with the
// upstream's description/category. A nil argument disables enrichment.
// Failures from the catalog are logged and dropped — enrichment is
// best-effort, never a hard dependency of the request.
func (a *InventoryApi) SetCatalog(c catalog.Client) {
	a.catalog = c
}

const (
	CtxKeyProduct CtxKey = "product"
)

func (a *InventoryApi) ConfigureRouter(r chi.Router) {
	r.HandleFunc("/subscribe", a.Subscribe)

	r.Route("/", func(r chi.Router) {
		r.With(httpx.Paginate).Get("/", a.List)
		r.Put("/", a.CreateProduct)

		r.Route("/{sku}", func(r chi.Router) {
			r.Use(a.ProductCtx)
			// productionEvent is the canonical write-path the
			// DSN-019 demo step exercises. Wrapping it with the
			// Idempotency-Key middleware (when wired) makes safe
			// retries replay the original response instead of
			// double-applying production.
			prod := http.HandlerFunc(a.CreateProductionEvent)
			if a.idempotency != nil {
				r.Method(http.MethodPut, "/productionEvent", a.idempotency(prod))
			} else {
				r.Put("/productionEvent", prod.ServeHTTP)
			}
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
	log.Ctx(r.Context()).Info().Msg("client requesting subscription")

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Ctx(r.Context()).Error().Err(err).Msg("failed to establish inventory subscription connection")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}
	go func() {
		defer conn.Close()

		ch := make(chan ProductInventory, 1)

		id := a.service.SubscribeInventory(ch)
		defer func() {
			a.service.UnsubscribeInventory(id)
		}()

		for inv := range ch {
			resp := &ProductResponse{ProductInventory: inv}
			body, err := json.Marshal(resp)
			if err != nil {
				log.Error().Err(err).Interface("clientId", id).Msg("failed to marshal product response")
				continue
			}

			log.Debug().Interface("clientId", id).Interface("productResponse", resp).Msg("sending inventory update to client")
			err = wsutil.WriteServerText(conn, body)
			if err != nil {
				log.Error().Err(err).Interface("clientId", id).Msg("failed to write server message, disconnecting client")
				return
			}
		}
	}()
}

// List returns a page of product inventory.
//
//	@Summary	List product inventory
//	@Tags		inventory
//	@Produce	json
//	@Param		limit	query		int	false	"max items per page (≤ 200)"	default(50)
//	@Param		offset	query		int	false	"page offset"					default(0)
//	@Success	200		{array}		ProductResponse
//	@Failure	400		{object}	httpx.Problem
//	@Failure	401		{object}	httpx.Problem
//	@Failure	500		{object}	httpx.Problem
//	@Header		200		{string}	Link	"RFC 8288 next/prev links"
//	@Router		/api/v1/inventory [get]
//	@Security	BearerAuth
func (a *InventoryApi) List(w http.ResponseWriter, r *http.Request) {
	p := httpx.PaginationFrom(r.Context())

	products, err := a.service.GetAllProductInventory(r.Context(), p.Limit, p.Offset)
	if err != nil {
		log.Ctx(r.Context()).Error().Err(err).Int("limit", p.Limit).Int("offset", p.Offset).Msg("failed to list product inventory")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}

	httpx.WriteLinkHeader(w, r, p, len(products))
	httpx.RenderList(w, r, NewProductListResponse(products))
}

// CreateProduct registers a new product.
//
//	@Summary	Create a product
//	@Tags		inventory
//	@Accept		json
//	@Produce	json
//	@Param		product	body		CreateProductRequest	true	"product to create"
//	@Success	201		{object}	ProductResponse
//	@Failure	400		{object}	httpx.Problem
//	@Failure	401		{object}	httpx.Problem
//	@Failure	500		{object}	httpx.Problem
//	@Router		/api/v1/inventory [put]
//	@Security	BearerAuth
func (a *InventoryApi) CreateProduct(w http.ResponseWriter, r *http.Request) {
	data := &CreateProductRequest{}
	if err := render.Bind(r, data); err != nil {
		httpx.Render(w, r, httpx.BadRequestProblem(err))
		return
	}

	if err := a.service.CreateProduct(r.Context(), data.Product); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			httpx.Render(w, r, httpx.BadRequestProblem(err))
			return
		}
		log.Ctx(r.Context()).Error().Err(err).Str("sku", data.Product.Sku).Msg("failed to create product")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}

	render.Status(r, http.StatusCreated)
	httpx.Render(w, r, NewProductResponse(ProductInventory{Product: data.Product}))
}

func (a *InventoryApi) ProductCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var product Product
		var err error

		sku := chi.URLParam(r, "sku")
		if sku == "" {
			httpx.Render(w, r, httpx.BadRequestProblem(errors.New("sku is required")))
			return
		}

		product, err = a.service.GetProduct(r.Context(), sku)
		if err != nil {
			if errors.Is(err, persistence.ErrNotFound) {
				httpx.Render(w, r, httpx.NotFoundProblem())
			} else {
				log.Ctx(r.Context()).Error().Err(err).Str("sku", sku).Msg("error acquiring product")
				httpx.Render(w, r, httpx.InternalServerProblem(err))
			}
			return
		}

		ctx := context.WithValue(r.Context(), CtxKeyProduct, product)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CreateProductionEvent records production of a SKU.
//
//	@Summary	Record a production event
//	@Tags		inventory
//	@Accept		json
//	@Produce	json
//	@Param		sku		path		string							true	"product SKU"
//	@Param		event	body		CreateProductionEventRequest	true	"production event"
//	@Success	201		{object}	ProductionEventResponse
//	@Failure	400		{object}	httpx.Problem
//	@Failure	401		{object}	httpx.Problem
//	@Failure	404		{object}	httpx.Problem
//	@Failure	500		{object}	httpx.Problem
//	@Router		/api/v1/inventory/{sku}/productionEvent [put]
//	@Security	BearerAuth
func (a *InventoryApi) CreateProductionEvent(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(Product)

	data := &CreateProductionEventRequest{}
	if err := render.Bind(r, data); err != nil {
		httpx.Render(w, r, httpx.BadRequestProblem(err))
		return
	}

	if err := a.service.Produce(r.Context(), product, *data.ProductionRequest); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			httpx.Render(w, r, httpx.BadRequestProblem(err))
			return
		}
		log.Ctx(r.Context()).Error().Err(err).Str("sku", product.Sku).Str("requestId", data.ProductionRequest.RequestID).Msg("failed to record production event")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}

	render.Status(r, http.StatusCreated)
	httpx.Render(w, r, &ProductionEventResponse{})
}

// GetProductInventory returns the current inventory for a SKU.
//
//	@Summary	Get product inventory
//	@Tags		inventory
//	@Produce	json
//	@Param		sku	path		string	true	"product SKU"
//	@Success	200	{object}	ProductResponse
//	@Failure	401	{object}	httpx.Problem
//	@Failure	404	{object}	httpx.Problem
//	@Failure	500	{object}	httpx.Problem
//	@Router		/api/v1/inventory/{sku} [get]
//	@Security	BearerAuth
func (a *InventoryApi) GetProductInventory(w http.ResponseWriter, r *http.Request) {
	product := r.Context().Value(CtxKeyProduct).(Product)

	res, err := a.service.GetProductInventory(r.Context(), product.Sku)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			httpx.Render(w, r, httpx.NotFoundProblem())
		} else {
			log.Ctx(r.Context()).Error().Err(err).Str("sku", product.Sku).Msg("failed to get product inventory")
			httpx.Render(w, r, httpx.InternalServerProblem(err))
		}
		return
	}

	resp := &ProductResponse{ProductInventory: res}
	resp.Catalog = a.lookupCatalog(r, product.Sku)
	render.Status(r, http.StatusOK)
	httpx.Render(w, r, resp)
}

// lookupCatalog runs the optional outbound enrichment (DSN-018). The
// catalog client is intentionally best-effort: missing data, 404s,
// upstream errors, and timeouts all fall through to a nil result so
// the inventory response still returns to the caller.
func (a *InventoryApi) lookupCatalog(r *http.Request, sku string) *CatalogInfo {
	if a.catalog == nil {
		return nil
	}
	p, err := a.catalog.Lookup(r.Context(), sku)
	if err != nil {
		if !errors.Is(err, catalog.ErrNotFound) {
			log.Ctx(r.Context()).Warn().Err(err).Str("sku", sku).Msg("catalog enrichment failed; serving unenriched response")
		}
		return nil
	}
	return &CatalogInfo{Description: p.Description, Category: p.Category}
}
