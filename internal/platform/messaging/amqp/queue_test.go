package amqp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
)

// TestNewMessage_CapturesRequestIDFromContext pins the producer-side
// half of DSN-005's AMQP correlation: NewMessage must snapshot the
// inbound request_id off ctx so the publisher goroutine can attach it
// to the AMQP headers after the request scope has ended.
func TestNewMessage_CapturesRequestIDFromContext(t *testing.T) {
	ctx := observability.ContextWithRequestID(context.Background(), "req-xyz")
	m := NewMessage(ctx, []byte(`{"sku":"abc"}`))
	if m.RequestID != "req-xyz" {
		t.Errorf("RequestID = %q, want %q", m.RequestID, "req-xyz")
	}
	if string(m.Body) != `{"sku":"abc"}` {
		t.Errorf("Body = %q, want body to be preserved", string(m.Body))
	}
}

func TestNewMessage_NoRequestIDIsEmpty(t *testing.T) {
	m := NewMessage(context.Background(), []byte("hi"))
	if m.RequestID != "" {
		t.Errorf("expected empty RequestID for ctx without one, got %q", m.RequestID)
	}
}

// TestEncodeEvent_WrapsPayloadInEnvelope pins the DSN-012 producer
// contract: bodies on the publish channels must be schema-conformant
// envelopes carrying event_id, event_type, event_version, occurred_at,
// and producer.
func TestEncodeEvent_WrapsPayloadInEnvelope(t *testing.T) {
	// Use an ad-hoc payload that satisfies the
	// inventory.product_inventory_changed v1 schema (upc, name, sku,
	// available all required). Schema knowledge lives outside this
	// package; tests here just verify EncodeEvent's envelope shape.
	in := map[string]any{
		"sku":       "sku1",
		"upc":       "upc1",
		"name":      "name1",
		"available": 5,
	}
	body, err := EncodeEvent(events.TypeProductInventoryChanged, in)
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

	var got map[string]any
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["sku"] != "sku1" || got["upc"] != "upc1" || got["name"] != "name1" {
		t.Errorf("payload round-trip lost fields: got=%+v want=%+v", got, in)
	}
}
