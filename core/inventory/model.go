// Package inventory is a rudimentary model that represents a fictional inventory tracking system for a factory. A real
// factory would obviously need much more fine grained detail and would probably use a different ubiquitous language.
package inventory

import (
	"github.com/pkg/errors"
	"time"
)

// ProductionRequest is a value object. A request to produce inventory.
type ProductionRequest struct {
	RequestID string `json:"requestID"`
	Quantity  int64 `json:"quantity"`
}

// ProductionEvent is an entity. An addition to inventory through production of a Product.
type ProductionEvent struct {
	ID        uint64    `json:"id"`
	RequestID string    `json:"requestID"`
	Sku       string    `json:"sku"`
	Quantity  int64     `json:"quantity"`
	Created   time.Time `json:"created"`
}

// Product is a value object. A SKU able to be produced by the factory.
type Product struct {
	Sku       string `json:"sku"`
	Upc       string `json:"upc"`
	Name      string `json:"name"`
}

// ProductInventory is an entity. It represents current inventory levels for the associated product.
type ProductInventory struct {
	Product
	Available int64  `json:"available"`
}

type ReserveState string

const (
	Open   ReserveState = "Open"
	Closed ReserveState = "Closed"
	None   ReserveState = ""
)

func ParseReserveState(v string) (ReserveState, error) {
	switch v {
	case string(Open):
		return Open, nil
	case string(Closed):
		return Closed, nil
	case string(None):
		return None, nil
	default:
		return None, errors.New("invalid reserve state")
	}
}

type ReservationRequest struct {
	RequestID string `json:"requestId"`
	Requester string `json:"requester"`
	Quantity int64 `json:"quantity"`
}

// Reservation is an entity. An amount of inventory set aside for a given Customer.
type Reservation struct {
	ID                uint64       `json:"id"`
	RequestID         string       `json:"requestId"`
	Requester         string       `json:"requester"`
	Sku               string       `json:"sku"`
	State             ReserveState `json:"state"`
	ReservedQuantity  int64        `json:"reservedQuantity"`
	RequestedQuantity int64        `json:"requestedQuantity"`
	Created           time.Time    `json:"created"`
}
