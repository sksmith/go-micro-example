# Messaging contracts

This service publishes and consumes domain events over RabbitMQ. The
on-the-wire shape and validation rules are owned by the
[`events`](../events) package — see also DSN-012 in `plan/`.

## Envelope

Every event is a JSON object matching
[`events/schemas/envelope.schema.json`](../events/schemas/envelope.schema.json):

```json
{
  "event_id": "00000000-0000-4000-8000-000000000001",
  "event_type": "inventory.product_inventory_changed",
  "event_version": 1,
  "occurred_at": "2026-05-11T12:00:00Z",
  "producer": "go-micro-example",
  "payload": { "sku": "abc", "upc": "123", "name": "Widget", "available": 5 }
}
```

| Field           | Purpose                                                                                                  |
| --------------- | -------------------------------------------------------------------------------------------------------- |
| `event_id`      | UUID v4. Stable per event instance — the natural idempotency key for consumers (see DSN-017 / DSN-025). |
| `event_type`    | Reverse-DNS-style kind identifier.                                                                       |
| `event_version` | Major schema version. Bumped only on breaking changes; see compatibility policy below.                   |
| `occurred_at`   | RFC 3339 timestamp of the source-of-truth event (not publish time).                                      |
| `producer`      | Logical name of the emitting service.                                                                    |
| `payload`       | Event-type-specific body. Validates against `<event_type>.v<event_version>.schema.json`.                 |

## Event types

| Type                                    | Producer                | Consumer                       |
| --------------------------------------- | ----------------------- | ------------------------------ |
| `inventory.product_inventory_changed`   | inventory write-path    | (none in this repo yet)        |
| `inventory.reservation_changed`         | inventory write-path    | (none in this repo yet)        |
| `inventory.product_created`             | upstream catalog system | `queue.ProductQueue` consumer  |

Adding a new event type means committing a new schema file under
`events/schemas/` and a `Type*` constant in `events/events.go`.

## Compatibility policy

- **Additive only within a major version.** Producers may add new
  optional fields, broaden numeric ranges, or add enum members
  (consumers must accept unknown enums gracefully). Producers may
  not remove fields, narrow types, tighten formats, rename fields,
  or change semantics without bumping `event_version`.
- **Breaking changes bump `event_version` and publish to a new
  exchange/topic.** Consumers migrate explicitly; there is no
  in-place "upgrade" of an existing topic. Two versions can run in
  parallel during migration.
- **`event_id` is stable and unique per event instance.** Consumers
  use it as the idempotency key and should keep at-least-once
  delivery semantics in mind.

## Consumer obligations

Every consumer MUST call
[`events.Validate`](../events/events.go) on the raw message body
before processing:

- A message that fails envelope or payload validation is routed to
  the queue's DLT/DLX with the validation error logged. The original
  body is preserved verbatim so operators can replay or inspect it.
- A message whose `event_type` does not match the queue's expected
  contract is also routed to DLT — silently dropping it would hide
  routing misconfigurations.

`queue.ProductQueue.handleProductMessage` is the reference
implementation; copy that shape for new consumers.

## Schema registry strategy

Schemas live in [`events/schemas/`](../events/schemas/) and ship with
the producing service. The compiled validator is loaded via
`go:embed`, so schemas are part of the binary and cannot drift from
the code that uses them.

This in-repo registry is intentional while there are fewer than 3
external consumers — the cognitive overhead of running a separate
registry isn't worth it yet. Once we cross that threshold, the
schemas are already in the right shape (JSON Schema draft 2020-12
with stable `$id`s) to upload to a network-attached registry
(Confluent Schema Registry, Buf Schema Registry, or an Apicurio
deployment).
