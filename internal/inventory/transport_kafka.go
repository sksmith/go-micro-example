package inventory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sksmith/go-micro-example/events"
	"github.com/sksmith/go-micro-example/kafka"
)

// InventoryEmitter adapts a *kafka.Producer to the EventEmitter
// interface so the inventory service can publish
// product-quantity-changed events to Kafka without importing
// franz-go.
type InventoryEmitter struct{ Producer *kafka.Producer }

// EmitProductQuantityChanged publishes an inventory.product_quantity_changed
// v1 event for the given SKU.
func (e *InventoryEmitter) EmitProductQuantityChanged(ctx context.Context, sku string, available int64) error {
	return e.Producer.Publish(ctx, events.TypeProductQuantityChanged, productQuantityChangedPayload{Sku: sku, Available: available})
}

type productQuantityChangedPayload struct {
	Sku       string `json:"sku"`
	Available int64  `json:"available"`
}

// InventoryCommandHandler decodes inventory commands off the inbound
// Kafka topic and dispatches them to the existing inventory service.
type InventoryCommandHandler struct {
	Service InventoryCommandTarget
}

// InventoryCommandTarget is the slice of Service the handler
// needs. The full service satisfies it; using a narrow interface
// keeps the kafka package off the core/inventory import graph in tests.
type InventoryCommandTarget interface {
	GetProduct(ctx context.Context, sku string) (Product, error)
	Produce(ctx context.Context, product Product, event ProductionRequest) error
}

// Handle implements Handler. Only inventory.record_production v1 is
// recognized today; unknown event types are an error and route to DLT.
func (h *InventoryCommandHandler) Handle(ctx context.Context, env events.Envelope) error {
	if env.EventType != events.TypeRecordProduction {
		return fmt.Errorf("kafka command handler: unsupported event_type %q", env.EventType)
	}
	var cmd recordProductionPayload
	if err := json.Unmarshal(env.Payload, &cmd); err != nil {
		return fmt.Errorf("decode record_production: %w", err)
	}
	product, err := h.Service.GetProduct(ctx, cmd.Sku)
	if err != nil {
		return fmt.Errorf("lookup product %q: %w", cmd.Sku, err)
	}
	return h.Service.Produce(ctx, product, ProductionRequest{RequestID: cmd.RequestID, Quantity: cmd.Quantity})
}

type recordProductionPayload struct {
	Sku       string `json:"sku"`
	RequestID string `json:"requestId"`
	Quantity  int64  `json:"quantity"`
}
