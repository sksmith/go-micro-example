// Package events defines the domain-event envelope and schema-backed
// validation used by go-micro-example's messaging contracts (DSN-012).
//
// The on-the-wire shape of every domain event is the Envelope struct
// below. The Payload field is the event-type-specific body; its shape
// is governed by the JSON Schema named
// "<event_type>.v<event_version>.schema.json" under events/schemas/.
//
// # Compatibility policy
//
//   - Additive changes (new optional fields, new enum values added to
//     consumers first) keep the same event_version. Producers must not
//     remove fields, narrow types, or change semantics without bumping
//     event_version.
//   - Breaking changes bump event_version AND publish to a new
//     exchange/topic. Consumers migrate explicitly; there is no
//     in-place "upgrade" of a topic.
//   - event_id is stable per event instance and is the idempotency key
//     for consumers.
//
// # Schema registry
//
// Schemas live in events/schemas/ as committed JSON Schema files. This
// is the "in-repo registry" — schemas are versioned with the
// producing service. We promote to a network-attached registry
// (Confluent Schema Registry, Buf Schema Registry) when there are 3+
// consumers; until then, schema-on-receive validation against the
// embedded files is sufficient.
package events

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Producer is the canonical service name used in Envelope.Producer.
const Producer = "go-micro-example"

// Reverse-DNS-style event-type identifiers. Add new types here so
// callers can't drift into stringly-typed names.
const (
	TypeProductInventoryChanged = "inventory.product_inventory_changed"
	TypeReservationChanged      = "inventory.reservation_changed"
	TypeProductCreated          = "inventory.product_created"
)

// Envelope is the RFC 7807-flavored common shape that wraps every
// domain event. See package doc for compatibility rules.
type Envelope struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventVersion int             `json:"event_version"`
	OccurredAt   time.Time       `json:"occurred_at"`
	Producer     string          `json:"producer"`
	Payload      json.RawMessage `json:"payload"`
}

// NewEnvelope constructs an Envelope around the given payload. The
// payload is marshaled to JSON immediately so callers fail fast if it
// is not serializable.
func NewEnvelope(eventID, eventType string, version int, occurredAt time.Time, payload any) (Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal payload: %w", err)
	}
	return Envelope{
		EventID:      eventID,
		EventType:    eventType,
		EventVersion: version,
		OccurredAt:   occurredAt.UTC(),
		Producer:     Producer,
		Payload:      body,
	}, nil
}

//go:embed schemas/*.json
var schemaFS embed.FS

var (
	registry    *jsonschema.Compiler
	registryMu  sync.Once
	errRegistry error
)

// compiler returns a singleton schema compiler with all in-repo
// schemas pre-loaded so Validate can resolve $id references locally.
func compiler() (*jsonschema.Compiler, error) {
	registryMu.Do(func() {
		c := jsonschema.NewCompiler()
		err := fs.WalkDir(schemaFS, "schemas", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			data, err := schemaFS.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			var doc any
			if err := json.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			m, ok := doc.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: top-level must be object", path)
			}
			id, _ := m["$id"].(string)
			if id == "" {
				return fmt.Errorf("%s: missing $id", path)
			}
			if err := c.AddResource(id, doc); err != nil {
				return fmt.Errorf("add %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			errRegistry = err
			return
		}
		registry = c
	})
	return registry, errRegistry
}

const (
	envelopeSchemaID = "https://github.com/sksmith/go-micro-example/events/schemas/envelope.schema.json"
	payloadSchemaFmt = "https://github.com/sksmith/go-micro-example/events/schemas/%s.v%d.schema.json"
)

// Validate parses raw JSON as an Envelope, validates the envelope
// shape against envelope.schema.json, and validates the payload
// against the schema named by event_type + event_version. Returns the
// parsed envelope on success; on failure, the error string is safe to
// emit on a DLT message so operators can see what was wrong.
func Validate(raw []byte) (Envelope, error) {
	c, err := compiler()
	if err != nil {
		return Envelope{}, fmt.Errorf("schema registry: %w", err)
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Envelope{}, fmt.Errorf("not valid JSON: %w", err)
	}

	envSchema, err := c.Compile(envelopeSchemaID)
	if err != nil {
		return Envelope{}, fmt.Errorf("compile envelope schema: %w", err)
	}
	if err := envSchema.Validate(doc); err != nil {
		return Envelope{}, fmt.Errorf("envelope invalid: %w", err)
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}

	payloadID := fmt.Sprintf(payloadSchemaFmt, env.EventType, env.EventVersion)
	payloadSchema, err := c.Compile(payloadID)
	if err != nil {
		return env, fmt.Errorf("unknown event_type/version %s v%d: %w", env.EventType, env.EventVersion, err)
	}

	var payloadDoc any
	if err := json.Unmarshal(env.Payload, &payloadDoc); err != nil {
		return env, fmt.Errorf("decode payload: %w", err)
	}
	if err := payloadSchema.Validate(payloadDoc); err != nil {
		return env, fmt.Errorf("payload invalid for %s v%d: %w", env.EventType, env.EventVersion, err)
	}

	return env, nil
}
