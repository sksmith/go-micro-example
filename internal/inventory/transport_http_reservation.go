package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

type ReservationService interface {
	Reserve(ctx context.Context, rr ReservationRequest) (Reservation, error)

	GetReservations(ctx context.Context, options GetReservationsOptions, limit, offset int) ([]Reservation, error)
	GetReservation(ctx context.Context, ID uint64) (Reservation, error)

	SubscribeReservations(ch chan<- Reservation) (id ReservationsSubID)
	UnsubscribeReservations(id ReservationsSubID)
}

type ReservationApi struct {
	service     ReservationService
	idempotency func(http.Handler) http.Handler
}

func NewReservationApi(service ReservationService) *ReservationApi {
	return &ReservationApi{service: service}
}

// SetIdempotency installs the optional Idempotency-Key middleware
// (DSN-019) on the reservation Create route. nil disables the
// middleware entirely.
func (a *ReservationApi) SetIdempotency(mw func(http.Handler) http.Handler) {
	a.idempotency = mw
}

const (
	CtxKeyReservation CtxKey = "reservation"
)

func (ra *ReservationApi) ConfigureRouter(r chi.Router) {
	r.HandleFunc("/subscribe", ra.Subscribe)

	r.Route("/", func(r chi.Router) {
		r.With(httpx.Paginate).Get("/", ra.List)
		create := http.HandlerFunc(ra.Create)
		if ra.idempotency != nil {
			r.Method(http.MethodPut, "/", ra.idempotency(create))
		} else {
			r.Put("/", create.ServeHTTP)
		}

		r.Route("/{ID}", func(r chi.Router) {
			r.Use(ra.ReservationCtx)
			r.Get("/", ra.Get)
			r.Delete("/", ra.Cancel)
		})
	})
}

func (a *ReservationApi) Subscribe(w http.ResponseWriter, r *http.Request) {
	log.Ctx(r.Context()).Info().Msg("client requesting subscription")

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Ctx(r.Context()).Error().Err(err).Msg("failed to establish reservation subscription connection")
		httpx.Render(w, r, httpx.InternalServerProblem(err))
		return
	}
	go func() {
		defer conn.Close()

		ch := make(chan Reservation, 1)

		id := a.service.SubscribeReservations(ch)
		defer func() {
			a.service.UnsubscribeReservations(id)
		}()

		for res := range ch {
			resp := &ReservationResponse{Reservation: res}

			body, err := json.Marshal(resp)
			if err != nil {
				log.Error().Err(err).Interface("clientId", id).Msg("failed to marshal reservation response")
				continue
			}

			log.Debug().Interface("clientId", id).Interface("reservationResponse", resp).Msg("sending reservation update to client")
			err = wsutil.WriteServerText(conn, body)
			if err != nil {
				log.Error().Err(err).Interface("clientId", id).Msg("failed to write server message, disconnecting client")
				return
			}
		}
	}()
}

// Get returns a single reservation by ID.
//
//	@Summary	Get a reservation
//	@Tags		reservation
//	@Produce	json
//	@Param		ID	path		int	true	"reservation ID"
//	@Success	200	{object}	ReservationResponse
//	@Failure	400	{object}	httpx.Problem
//	@Failure	401	{object}	httpx.Problem
//	@Failure	404	{object}	httpx.Problem
//	@Failure	500	{object}	httpx.Problem
//	@Router		/api/v1/reservation/{ID} [get]
//	@Security	BearerAuth
func (a *ReservationApi) Get(w http.ResponseWriter, r *http.Request) {
	res := r.Context().Value(CtxKeyReservation).(Reservation)

	resp := &ReservationResponse{Reservation: res}
	render.Status(r, http.StatusOK)
	httpx.Render(w, r, resp)
}

// Create reserves inventory for a SKU.
//
//	@Summary	Create a reservation
//	@Tags		reservation
//	@Accept		json
//	@Produce	json
//	@Param		reservation	body		ReservationRequestDto	true	"reservation request"
//	@Success	201			{object}	ReservationResponse
//	@Failure	400			{object}	httpx.Problem
//	@Failure	401			{object}	httpx.Problem
//	@Failure	404			{object}	httpx.Problem
//	@Failure	500			{object}	httpx.Problem
//	@Router		/api/v1/reservation [post]
//	@Security	BearerAuth
func (a *ReservationApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &ReservationRequestDto{}
	if err := render.Bind(r, data); err != nil {
		httpx.Render(w, r, httpx.BadRequestProblem(err))
		return
	}

	res, err := a.service.Reserve(r.Context(), *data.ReservationRequest)

	if err != nil {
		switch {
		case errors.Is(err, persistence.ErrNotFound):
			httpx.Render(w, r, httpx.NotFoundProblem())
		case errors.Is(err, ErrInvalidInput):
			httpx.Render(w, r, httpx.BadRequestProblem(err))
		default:
			log.Ctx(r.Context()).Error().Err(err).Interface("reservationRequest", data).Msg("failed to reserve")
			httpx.Render(w, r, httpx.InternalServerProblem(err))
		}
		return
	}

	resp := &ReservationResponse{Reservation: res}
	render.Status(r, http.StatusCreated)
	httpx.Render(w, r, resp)
}

func (a *ReservationApi) Cancel(_ http.ResponseWriter, _ *http.Request) {
	// TODO Not implemented
}

func (a *ReservationApi) ReservationCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		IDStr := chi.URLParam(r, "ID")
		if IDStr == "" {
			httpx.Render(w, r, httpx.BadRequestProblem(errors.New("reservation id is required")))
			return
		}

		ID, err := strconv.ParseUint(IDStr, 10, 64)
		if err != nil {
			log.Ctx(r.Context()).Error().Err(err).Str("ID", IDStr).Msg("invalid reservation id")
			httpx.Render(w, r, httpx.BadRequestProblem(errors.New("invalid reservation id")))
			return
		}

		reservation, err := a.service.GetReservation(r.Context(), ID)

		if err != nil {
			if errors.Is(err, persistence.ErrNotFound) {
				httpx.Render(w, r, httpx.NotFoundProblem())
			} else {
				log.Ctx(r.Context()).Error().Err(err).Str("id", IDStr).Msg("error acquiring product")
				httpx.Render(w, r, httpx.InternalServerProblem(err))
			}
			return
		}

		ctx := context.WithValue(r.Context(), CtxKeyReservation, reservation)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// List returns reservations, optionally filtered by sku and state.
//
//	@Summary	List reservations
//	@Tags		reservation
//	@Produce	json
//	@Param		sku		query		string	false	"filter by SKU"
//	@Param		state	query		string	false	"filter by state"	Enums(Open, Closed)
//	@Param		limit	query		int		false	"max items per page (≤ 200)"	default(50)
//	@Param		offset	query		int		false	"page offset"					default(0)
//	@Success	200		{array}		ReservationResponse
//	@Failure	400		{object}	httpx.Problem
//	@Failure	401		{object}	httpx.Problem
//	@Failure	404		{object}	httpx.Problem
//	@Failure	500		{object}	httpx.Problem
//	@Header		200		{string}	Link	"RFC 8288 next/prev links"
//	@Router		/api/v1/reservation [get]
//	@Security	BearerAuth
func (a *ReservationApi) List(w http.ResponseWriter, r *http.Request) {
	p := httpx.PaginationFrom(r.Context())

	sku := r.URL.Query().Get("sku")

	state, err := ParseReserveState(r.URL.Query().Get("state"))
	if err != nil {
		httpx.Render(w, r, httpx.BadRequestProblem(errors.New("invalid state")))
		return
	}

	res, err := a.service.GetReservations(r.Context(), GetReservationsOptions{Sku: sku, State: state}, p.Limit, p.Offset)

	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			httpx.Render(w, r, httpx.NotFoundProblem())
		} else {
			log.Ctx(r.Context()).Error().Err(err).Str("sku", sku).Str("state", string(state)).Msg("failed to list reservations")
			httpx.Render(w, r, httpx.InternalServerProblem(err))
		}
		return
	}

	httpx.WriteLinkHeader(w, r, p, len(res))

	resList := NewReservationListResponse(res)
	render.Status(r, http.StatusOK)
	httpx.RenderList(w, r, resList)
}
