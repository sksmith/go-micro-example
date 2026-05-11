package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/observability"
	"github.com/sksmith/go-micro-example/events"
)

// TestNewMessage_CapturesRequestIDFromContext pins the producer-side
// half of DSN-005's AMQP correlation: PublishInventory/PublishReservation
// must snapshot the inbound request_id off ctx so the publisher
// goroutine can attach it to the AMQP headers after the request scope
// has ended.
func TestNewMessage_CapturesRequestIDFromContext(t *testing.T) {
	ctx := observability.ContextWithRequestID(context.Background(), "req-xyz")
	m := newMessage(ctx, []byte(`{"sku":"abc"}`))
	if m.requestID != "req-xyz" {
		t.Errorf("requestID = %q, want %q", m.requestID, "req-xyz")
	}
	if string(m.body) != `{"sku":"abc"}` {
		t.Errorf("body = %q, want body to be preserved", string(m.body))
	}
}

func TestNewMessage_NoRequestIDIsEmpty(t *testing.T) {
	m := newMessage(context.Background(), []byte("hi"))
	if m.requestID != "" {
		t.Errorf("expected empty requestID for ctx without one, got %q", m.requestID)
	}
}

// TestEncodeEvent_WrapsPayloadInEnvelope pins the DSN-012 producer
// contract: bodies on the inventory/reservation channels must be
// schema-conformant envelopes carrying event_id, event_type,
// event_version, occurred_at, and producer.
func TestEncodeEvent_WrapsPayloadInEnvelope(t *testing.T) {
	pi := inventory.ProductInventory{
		Product:   inventory.Product{Sku: "sku1", Upc: "upc1", Name: "name1"},
		Available: 5,
	}
	body, err := encodeEvent(events.TypeProductInventoryChanged, pi)
	if err != nil {
		t.Fatal(err)
	}
	env, err := events.Validate(body)
	if err != nil {
		t.Fatalf("encoded payload failed schema validation: %v", err)
	}
	if env.EventType != events.TypeProductInventoryChanged {
		t.Errorf("event_type got=%q", env.EventType)
	}
	if env.EventVersion != 1 {
		t.Errorf("event_version got=%d want=1", env.EventVersion)
	}
	if env.EventID == "" {
		t.Error("event_id should be populated for consumer idempotency")
	}
	if env.OccurredAt.IsZero() {
		t.Error("occurred_at should be populated")
	}
	if env.Producer != events.Producer {
		t.Errorf("producer got=%q want=%q", env.Producer, events.Producer)
	}

	var got inventory.ProductInventory
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got != pi {
		t.Errorf("payload round-trip got=%+v want=%+v", got, pi)
	}
}

type productHandlerStub struct {
	called     int
	lastSku    string
	err        error
	lastCtxReq string
}

func (s *productHandlerStub) CreateProduct(ctx context.Context, p inventory.Product) error {
	s.called++
	s.lastSku = p.Sku
	s.lastCtxReq = observability.RequestIDFromContext(ctx)
	return s.err
}

// newProductQueueForTest builds a ProductQueue with a buffered DLT
// channel so handleProductMessage's send-to-DLT is observable from
// the test goroutine without standing up AMQP.
func newProductQueueForTest() (*ProductQueue, chan message) {
	dlt := make(chan message, 4)
	pq := &ProductQueue{productDlt: dlt}
	return pq, dlt
}

func TestHandleProductMessage_ValidEventReachesHandler(t *testing.T) {
	pq, dlt := newProductQueueForTest()
	h := &productHandlerStub{}

	body, err := encodeEvent(events.TypeProductCreated, inventory.Product{Sku: "sku1", Upc: "u", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.ContextWithRequestID(context.Background(), "req-1")
	pq.handleProductMessage(ctx, h, message{body: body, requestID: "req-1"})

	if h.called != 1 {
		t.Errorf("handler should have been called once, got %d", h.called)
	}
	if h.lastSku != "sku1" {
		t.Errorf("decoded sku=%q want=sku1", h.lastSku)
	}
	if h.lastCtxReq != "req-1" {
		t.Errorf("handler ctx request_id=%q want=req-1", h.lastCtxReq)
	}
	if len(dlt) != 0 {
		t.Errorf("DLT should be empty, has %d messages", len(dlt))
	}
}

func TestHandleProductMessage_InvalidEnvelopeRoutedToDLT(t *testing.T) {
	pq, dlt := newProductQueueForTest()
	h := &productHandlerStub{}

	bad := []byte(`{"not":"an envelope"}`)
	pq.handleProductMessage(context.Background(), h, message{body: bad})

	if h.called != 0 {
		t.Errorf("handler should not be called on invalid event")
	}
	if len(dlt) != 1 {
		t.Fatalf("expected 1 DLT message, got %d", len(dlt))
	}
	got := <-dlt
	if string(got.body) != string(bad) {
		t.Errorf("DLT should preserve original body verbatim")
	}
}

func TestHandleProductMessage_WrongEventTypeRoutedToDLT(t *testing.T) {
	pq, dlt := newProductQueueForTest()
	h := &productHandlerStub{}

	body, _ := encodeEvent(events.TypeProductInventoryChanged, inventory.ProductInventory{
		Product: inventory.Product{Sku: "s", Upc: "u", Name: "n"}, Available: 1,
	})
	pq.handleProductMessage(context.Background(), h, message{body: body})

	if h.called != 0 {
		t.Errorf("handler should not be called for wrong event_type")
	}
	if len(dlt) != 1 {
		t.Errorf("expected wrong-type message on DLT, got %d", len(dlt))
	}
}

func TestHandleProductMessage_HandlerErrorRoutesToDLT(t *testing.T) {
	pq, _ := newProductQueueForTest()
	h := &productHandlerStub{err: errors.New("downstream failure")}

	body, _ := encodeEvent(events.TypeProductCreated, inventory.Product{Sku: "s", Upc: "u", Name: "n"})
	// productDlt is unbuffered here? newProductQueueForTest set buffered cap=4.
	pq.handleProductMessage(context.Background(), h, message{body: body, requestID: "r"})

	if h.called != 1 {
		t.Errorf("handler should have been called, got %d", h.called)
	}
	if len(pq.productDlt) != 1 {
		t.Errorf("handler error should send to DLT, DLT has %d", len(pq.productDlt))
	}
}
