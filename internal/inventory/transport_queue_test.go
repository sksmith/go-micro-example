package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/messaging/amqp"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
)

type productHandlerStub struct {
	called     int
	lastSku    string
	err        error
	lastCtxReq string
}

func (s *productHandlerStub) CreateProduct(ctx context.Context, p Product) error {
	s.called++
	s.lastSku = p.Sku
	s.lastCtxReq = observability.RequestIDFromContext(ctx)
	return s.err
}

// newProductQueueForTest builds a ProductQueue with a buffered DLT
// channel so handleProductMessage's send-to-DLT is observable from
// the test goroutine without standing up AMQP.
func newProductQueueForTest() (*ProductQueue, chan amqp.Message) {
	dlt := make(chan amqp.Message, 4)
	pq := &ProductQueue{productDlt: dlt}
	return pq, dlt
}

func TestHandleProductMessage_ValidEventReachesHandler(t *testing.T) {
	pq, dlt := newProductQueueForTest()
	h := &productHandlerStub{}

	body, err := amqp.EncodeEvent(events.TypeProductCreated, Product{Sku: "sku1", Upc: "u", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.ContextWithRequestID(context.Background(), "req-1")
	pq.handleProductMessage(ctx, h, amqp.Message{Body: body, RequestID: "req-1"})

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
	pq.handleProductMessage(context.Background(), h, amqp.Message{Body: bad})

	if h.called != 0 {
		t.Errorf("handler should not be called on invalid event")
	}
	if len(dlt) != 1 {
		t.Fatalf("expected 1 DLT message, got %d", len(dlt))
	}
	got := <-dlt
	if string(got.Body) != string(bad) {
		t.Errorf("DLT should preserve original body verbatim")
	}
}

func TestHandleProductMessage_WrongEventTypeRoutedToDLT(t *testing.T) {
	pq, dlt := newProductQueueForTest()
	h := &productHandlerStub{}

	body, _ := amqp.EncodeEvent(events.TypeProductInventoryChanged, ProductInventory{
		Product: Product{Sku: "s", Upc: "u", Name: "n"}, Available: 1,
	})
	pq.handleProductMessage(context.Background(), h, amqp.Message{Body: body})

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

	body, _ := amqp.EncodeEvent(events.TypeProductCreated, Product{Sku: "s", Upc: "u", Name: "n"})
	pq.handleProductMessage(context.Background(), h, amqp.Message{Body: body, RequestID: "r"})

	if h.called != 1 {
		t.Errorf("handler should have been called, got %d", h.called)
	}
	if len(pq.productDlt) != 1 {
		t.Errorf("handler error should send to DLT, DLT has %d", len(pq.productDlt))
	}
}

// TestInventoryQueue_PingFlow pins the TST-004 contract end-to-end on
// the InventoryQueue side: zero-value state is unready, sessionOK
// flips it to ready immediately, and a backdated session falls back
// to unready once it crosses the staleness window. Driving the
// atomic directly avoids real-time sleeps in CI.
func TestInventoryQueue_PingFlow(t *testing.T) {
	iq := &InventoryQueue{}

	if err := iq.Ping(context.Background()); !errors.Is(err, errAMQPNeverConnected) {
		t.Errorf("ping before first session = %v, want errAMQPNeverConnected", err)
	}

	iq.sessionOK()
	if err := iq.Ping(context.Background()); err != nil {
		t.Errorf("ping right after sessionOK = %v, want nil", err)
	}

	// Backdate to just past the staleness boundary.
	iq.lastSessionAt.Store(time.Now().Add(-amqpSessionStaleAfter - time.Second).UnixNano())
	if err := iq.Ping(context.Background()); err == nil {
		t.Error("ping past staleness window = nil, want stale-session error")
	}
}

// TestProductQueue_PingFlow mirrors the InventoryQueue test for the
// consumer side: a single queue value tracks both the Subscribe loop
// and the DLT publish loop, so Ping reports ready as soon as either
// has obtained a session.
func TestProductQueue_PingFlow(t *testing.T) {
	pq := &ProductQueue{}

	if err := pq.Ping(context.Background()); !errors.Is(err, errAMQPNeverConnected) {
		t.Errorf("ping before first session = %v, want errAMQPNeverConnected", err)
	}

	pq.sessionOK()
	if err := pq.Ping(context.Background()); err != nil {
		t.Errorf("ping right after sessionOK = %v, want nil", err)
	}

	pq.lastSessionAt.Store(time.Now().Add(-amqpSessionStaleAfter - time.Second).UnixNano())
	if err := pq.Ping(context.Background()); err == nil {
		t.Error("ping past staleness window = nil, want stale-session error")
	}
}
