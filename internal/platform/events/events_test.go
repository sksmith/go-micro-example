package events_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/internal/platform/events"
)

func TestValidateAcceptsWellFormedEvent(t *testing.T) {
	env, err := events.NewEnvelope(
		"00000000-0000-4000-8000-000000000001",
		events.TypeProductCreated,
		1,
		time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
		map[string]any{"sku": "sku1", "upc": "1234", "name": "thing"},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	got, err := events.Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.EventID != env.EventID {
		t.Errorf("event_id round-trip: got=%q want=%q", got.EventID, env.EventID)
	}
	if got.Producer != events.Producer {
		t.Errorf("producer got=%q want=%q", got.Producer, events.Producer)
	}
}

func TestValidateRejectsMissingEnvelopeField(t *testing.T) {
	raw := []byte(`{"event_id":"x","event_type":"inventory.product_created","event_version":1,"occurred_at":"2026-05-11T12:00:00Z","payload":{"sku":"s","upc":"u","name":"n"}}`)
	_, err := events.Validate(raw)
	if err == nil {
		t.Fatal("expected envelope validation to fail (missing producer)")
	}
	if !strings.Contains(err.Error(), "envelope") {
		t.Errorf("error should mention envelope, got: %v", err)
	}
}

func TestValidateRejectsBadOccurredAt(t *testing.T) {
	raw := []byte(`{"event_id":"x","event_type":"inventory.product_created","event_version":1,"occurred_at":"not-a-timestamp","producer":"p","payload":{"sku":"s","upc":"u","name":"n"}}`)
	_, err := events.Validate(raw)
	if err == nil {
		t.Fatal("expected validation to fail on bad timestamp")
	}
}

func TestValidateRejectsUnknownEventType(t *testing.T) {
	env, _ := events.NewEnvelope("id", "inventory.never_heard_of_it", 1, time.Now(), map[string]any{})
	raw, _ := json.Marshal(env)
	_, err := events.Validate(raw)
	if err == nil {
		t.Fatal("expected unknown event_type to fail")
	}
	if !strings.Contains(err.Error(), "unknown event_type") {
		t.Errorf("error should call out unknown type, got: %v", err)
	}
}

func TestValidateRejectsInvalidPayload(t *testing.T) {
	// missing required sku
	env, _ := events.NewEnvelope("id", events.TypeProductCreated, 1, time.Now(),
		map[string]any{"upc": "1234", "name": "thing"})
	raw, _ := json.Marshal(env)
	_, err := events.Validate(raw)
	if err == nil {
		t.Fatal("expected payload validation to fail")
	}
	if !strings.Contains(err.Error(), "payload") {
		t.Errorf("error should mention payload, got: %v", err)
	}
}

func TestValidateRejectsNonJSON(t *testing.T) {
	_, err := events.Validate([]byte("not-json"))
	if err == nil {
		t.Fatal("expected non-JSON to fail")
	}
}
