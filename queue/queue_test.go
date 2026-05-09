package queue

import (
	"context"
	"testing"

	"github.com/sksmith/go-micro-example/core/observability"
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
