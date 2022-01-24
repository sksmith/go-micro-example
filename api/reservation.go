package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
)

type ReservationApi struct {
	service inventory.Service
}

func NewReservationApi(service inventory.Service) *ReservationApi {
	return &ReservationApi{service: service}
}

const (
	CtxKeyReservation CtxKey = "reservation"
)

func (ra *ReservationApi) ConfigureRouter(r chi.Router) {
	r.HandleFunc("/subscribe", ra.Subscribe)

	r.Route("/", func(r chi.Router) {
		r.With(Paginate).Get("/", ra.List)
		r.Put("/", ra.Create)

		r.Route("/{ID}", func(r chi.Router) {
			r.Use(ra.ReservationCtx)
			r.Get("/", ra.Get)
			r.Delete("/", ra.Cancel)
		})
	})
}

func (a *ReservationApi) Subscribe(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("client requesting subscription")

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Err(err).Msg("failed to establish inventory subscription connection")
		Render(w, r, ErrInternalServer)
	}
	go func() {
		defer conn.Close()

		ch := make(chan inventory.Reservation, 1)

		id := a.service.SubscribeReservations(ch)
		defer func() {
			a.service.UnsubscribeReservations(id)
		}()

		for res := range ch {
			resp := &ReservationResponse{Reservation: res}

			body, err := json.Marshal(resp)
			if err != nil {
				log.Err(err).Interface("clientId", id).Msg("failed to marshal product response")
				continue
			}

			log.Debug().Interface("clientId", id).Interface("reservationResponse", resp).Msg("sending reservation update to client")
			err = wsutil.WriteServerText(conn, body)
			if err != nil {
				log.Err(err).Interface("clientId", id).Msg("failed to write server message, disconnecting client")
				return
			}
		}
	}()
}

func (a *ReservationApi) Get(w http.ResponseWriter, r *http.Request) {
	res := r.Context().Value(CtxKeyProduct).(inventory.Reservation)

	resp := &ReservationResponse{Reservation: res}
	render.Status(r, http.StatusOK)
	Render(w, r, resp)
}

func (a *ReservationApi) Create(w http.ResponseWriter, r *http.Request) {
	data := &ReservationRequest{}
	if err := render.Bind(r, data); err != nil {
		Render(w, r, ErrInvalidRequest(err))
		return
	}

	res, err := a.service.Reserve(r.Context(), *data.ReservationRequest)
	if err != nil {
		log.Error().Stack().Err(err).Msg("failed to reserve")
		Render(w, r, ErrInternalServer)
		return
	}

	resp := &ReservationResponse{Reservation: res}
	render.Status(r, http.StatusCreated)
	Render(w, r, resp)
}

func (a *ReservationApi) Cancel(_ http.ResponseWriter, _ *http.Request) {
	// TODO Not implemented
}

func (a *ReservationApi) ReservationCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		IDStr := chi.URLParam(r, "ID")
		if IDStr == "" {
			Render(w, r, ErrInvalidRequest(errors.New("reservation id is required")))
			return
		}

		ID, err := strconv.ParseUint(IDStr, 10, 64)
		if err != nil {
			log.Error().Err(err).Str("ID", IDStr).Msg("invalid reservation id")
			Render(w, r, ErrInvalidRequest(errors.New("invalid reservation id")))
		}

		reservation, err := a.service.GetReservation(r.Context(), ID)

		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				Render(w, r, ErrNotFound)
			} else {
				log.Error().Err(err).Str("id", IDStr).Msg("error acquiring product")
				Render(w, r, ErrInternalServer)
			}
			return
		}

		ctx := context.WithValue(r.Context(), CtxKeyReservation, reservation)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *ReservationApi) List(w http.ResponseWriter, r *http.Request) {
	limit := r.Context().Value(CtxKeyLimit).(int)
	offset := r.Context().Value(CtxKeyOffset).(int)

	sku := r.URL.Query().Get("sku")

	state, err := inventory.ParseReserveState(r.URL.Query().Get("state"))
	if err != nil {
		Render(w, r, ErrInvalidRequest(errors.New("invalid state")))
		return
	}

	res, err := a.service.GetReservations(r.Context(), inventory.GetReservationsOptions{Sku: sku, State: state}, limit, offset)

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
