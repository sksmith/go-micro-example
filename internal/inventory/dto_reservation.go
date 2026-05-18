package inventory

import (
	"errors"
	"net/http"
)

type ReservationRequestDto struct {
	*ReservationRequest
} // @name ReservationRequestDto

func (r *ReservationRequestDto) Bind(_ *http.Request) error {
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
	Reservation
} // @name ReservationResponse

func (r *ReservationResponse) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}
