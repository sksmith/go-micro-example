package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/events"
	"github.com/sksmith/go-micro-example/internal/inventory"
)

type fakeInventory struct {
	getCalls     int
	produceCalls int
	lastSku      string
	lastQty      int64
	getErr       error
	produceErr   error
}

func (f *fakeInventory) GetProduct(_ context.Context, sku string) (inventory.Product, error) {
	f.getCalls++
	f.lastSku = sku
	if f.getErr != nil {
		return inventory.Product{}, f.getErr
	}
	return inventory.Product{Sku: sku, Upc: "u", Name: "n"}, nil
}

func (f *fakeInventory) Produce(_ context.Context, product inventory.Product, event inventory.ProductionRequest) error {
	f.produceCalls++
	f.lastQty = event.Quantity
	return f.produceErr
}

func TestInventoryCommandHandlerHappyPath(t *testing.T) {
	fake := &fakeInventory{}
	h := &inventory.InventoryCommandHandler{Service: fake}

	env, err := events.NewEnvelope(
		"event-1",
		events.TypeRecordProduction,
		1,
		time.Now(),
		map[string]any{"sku": "sku-1", "requestId": "req-1", "quantity": 5},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Handle(context.Background(), env); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fake.getCalls != 1 || fake.lastSku != "sku-1" {
		t.Errorf("GetProduct calls=%d sku=%q", fake.getCalls, fake.lastSku)
	}
	if fake.produceCalls != 1 || fake.lastQty != 5 {
		t.Errorf("Produce calls=%d qty=%d", fake.produceCalls, fake.lastQty)
	}
}

func TestInventoryCommandHandlerRejectsUnknownType(t *testing.T) {
	h := &inventory.InventoryCommandHandler{Service: &fakeInventory{}}
	env, _ := events.NewEnvelope("e", "inventory.never_heard_of_it", 1, time.Now(), struct{}{})
	err := h.Handle(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unknown event_type")
	}
}

func TestInventoryCommandHandlerSurfacesGetError(t *testing.T) {
	fake := &fakeInventory{getErr: errors.New("not found")}
	h := &inventory.InventoryCommandHandler{Service: fake}
	env, _ := events.NewEnvelope("e", events.TypeRecordProduction, 1, time.Now(),
		map[string]any{"sku": "missing", "requestId": "r", "quantity": 1})
	err := h.Handle(context.Background(), env)
	if err == nil {
		t.Fatal("expected error when GetProduct fails")
	}
	if fake.produceCalls != 0 {
		t.Error("Produce should not run when GetProduct fails")
	}
}

func TestInventoryCommandHandlerSurfacesBadPayload(t *testing.T) {
	h := &inventory.InventoryCommandHandler{Service: &fakeInventory{}}
	env := events.Envelope{
		EventID:      "e",
		EventType:    events.TypeRecordProduction,
		EventVersion: 1,
		OccurredAt:   time.Now(),
		Producer:     "test",
		Payload:      json.RawMessage(`"not-an-object"`),
	}
	err := h.Handle(context.Background(), env)
	if err == nil {
		t.Fatal("expected decode error on malformed payload")
	}
}
