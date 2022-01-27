package api

import (
	"errors"
	"net/http"

	"github.com/sksmith/go-micro-example/core/inventory"
)

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
