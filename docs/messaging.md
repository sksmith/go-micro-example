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

| Type | Transport | Producer | Consumer |
| --- | --- | --- | --- |
| `inventory.product_inventory_changed` | AMQP fanout | inventory write-path | (none in this repo yet) |
| `inventory.reservation_changed` | AMQP fanout | inventory write-path | (none in this repo yet) |
| `inventory.product_created` | AMQP queue | upstream catalog system | `queue.ProductQueue` consumer |
| `inventory.product_quantity_changed` | Kafka topic | inventory write-path (DSN-016) | downstream subscribers |
| `inventory.record_production` | Kafka topic | demo runner / upstream caller | `kafka.InventoryCommandHandler` |

Adding a new event type means committing a new schema file under
`events/schemas/` and a `Type*` constant in `events/events.go`.

## Kafka (DSN-016)

The Kafka transport runs in parallel to AMQP: producers emit on
`inventory.product-quantity-changed.v1` whenever inventory changes, and
a consumer joins the `inventory-service` group on
`inventory.commands.v1` to apply inbound commands (currently
`inventory.record_production`).

Wire-level details:

- **Topic naming**: `<domain>.<event-or-command>.<version>` per the
  acceptance criteria.
- **Body**: same `events.Envelope` JSON used by AMQP. Schemas
  validate on receipt.
- **Headers**: `event_id` (UUID v4) for at-a-glance lookup and
  `traceparent` (W3C) so consumer spans stitch back to the producer.
- **Retries**: bounded in-memory (default 3 with exponential
  backoff). On exhaustion the message is republished to
  `inventory.commands.v1.dlt` with an `x-dlt-reason` header and the
  offset is committed so the consumer doesn't get stuck.
- **At-least-once delivery, at-most-once handler invocation**: the
  Kafka consumer commits offsets only after the handler succeeds, so
  a crash mid-handler causes redelivery. DSN-017 wraps the handler
  with the [`idempotency`](../idempotency) package's `Applier`: each
  message's `event_id` is recorded in `processed_events` before the
  handler runs. A redelivery hits the unique constraint on
  `(event_id, consumer_group)`, rowcount=0, and the handler is
  skipped. On handler error the dedupe row is removed so the next
  delivery can re-attempt.
- **Prometheus counters**: `kafka_events_produced_total`,
  `kafka_events_consumed_total`, `kafka_events_failed_total`,
  `kafka_events_dlt_total`.

The Kafka path is gated by `kafka.brokers`. Leave it empty
(`GME_KAFKA_BROKERS=""`) to run the service without Kafka — the
AMQP path keeps working unchanged.

### Consumer guarantees (DSN-017)

**What the consumer guarantees:**

- The handler runs **at most once per `(event_id, consumer_group)` pair**
  under normal operation. Redeliveries — rebalance churn, request
  retries from upstream, manual replay — are dropped at the door.
- On handler error the dedupe row is removed so the next delivery can
  re-attempt cleanly.
- Two independent consumer groups can each apply the same event once
  (the dedupe key is per-group).

**What the consumer does NOT guarantee:**

- Atomicity between dedupe row and handler side effects. The dedupe
  row commits BEFORE the handler runs (not inside the same
  transaction), so a process crash AFTER INSERT but BEFORE the
  rollback-on-error DELETE leaves a stuck row that skips the next
  retry. This is a deliberate trade-off — co-transactional dedupe
  would require threading a transaction through every service method
  the handler touches. If you need that guarantee, push the
  idempotency check INTO the service (use `event_id` as part of the
  business-level uniqueness key, like `request_id` on the inventory
  service's `Produce` method).
- Ordering across partitions or across topics. Kafka guarantees order
  within a partition; the dedupe layer doesn't change that.
- Replay correctness for handlers with side effects **outside**
  Postgres. The Applier only records the dedupe row — external HTTP
  calls, file writes, etc. happen exactly as many times as the
  handler runs.
- Indefinite retention. `processed_events` rows older than the
  retention window (default 30 days) are pruned by a background
  goroutine started alongside the consumer. Events redelivered after
  the retention window are NOT recognized as duplicates.

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

## RabbitMQ transport (TST-003)

The broker plumbing lives in
[`internal/platform/messaging/amqp`](../internal/platform/messaging/amqp).
The package's API is three top-level functions that compose into the
queue subsystem:

- `Redial(ctx, url) chan chan Session` repeatedly dials the broker
  and yields fresh `(connection, channel)` pairs as `Session`
  values. The consumer (the publish or subscribe loop) reads a
  `sess` channel off the outer channel, then reads exactly one
  `Session` off `sess` per outer iteration. On dial failure the
  loop retries internally with a 2-second back-off — it never
  re-offers `sess` to a fresh consumer mid-attempt, which would
  deadlock anyone already parked at `<-session`.
- `Publish(sessions, exchange, messages, onSession)` consumes from
  `messages` and publishes to `exchange` using sessions from
  `Redial`. After each successful send it waits for the broker's
  publisher-confirm; the producer span ends with `codes.Ok` on Ack,
  `codes.Error` on Nack, publish error, or confirm-channel close.
  `onSession` fires each time a fresh session is acquired (powers
  the `/ready` AMQP pinger from TST-004).
- `Subscribe(sessions, queue, messages, onSession)` consumes
  deliveries from `queue` and forwards them onto `messages`. Each
  delivery is Ack'd individually; an Ack failure is logged but the
  loop keeps running — connection-level failures surface as the
  inner `range deliveries` ending, at which point the outer loop
  pulls a fresh session.

### Reconnection

Reconnection is implicit. Every publish/subscribe call sites the
`Redial(...)` channel into the loop directly:

```go
go amqp.Publish(amqp.Redial(ctx, url), exch, ch, onSession)
```

When the publish loop's session dies (publish error, confirm
channel closes, broker hangs up) the inner loop breaks and the
outer `for session := range sessions` re-iterates, pulling a fresh
session from `Redial`. The redial back-off bounds reconnect storm.
Tests in
[`queue_test.go`](../internal/platform/messaging/amqp/queue_test.go)
drive this with a scripted `Dialer` fake — see
`TestRedial_DialFailureRetries` and
`TestRedial_CtxCancelDuringBackoffExitsCleanly`.

### Publisher confirms

`Publish` enables RabbitMQ's publisher-confirm extension via
`Channel.Confirm(false)`. Each in-flight body waits for an Ack
before the loop reads the next message. This trades throughput for
durability — the producer span (DSN-004a) only marks Ok once the
broker has actually persisted the message.

When the broker reports "publisher confirms not supported"
(`Channel.Confirm` returns an error), the loop closes its internal
confirm channel and bails out of the inner loop — effectively
treating the not-supported path as a session-loss event. That's a
deliberate "don't fall back to fire-and-forget silently" stance:
the only AMQP broker this service is tested against is RabbitMQ,
which supports confirms. A non-confirming broker shows up as a hot
reconnect loop in metrics, which is what we want — it's a
configuration error worth being loud about.
`TestPublish_ConfirmsNotSupportedFallback` pins that exit path.

### What this code does NOT guarantee

- **Exactly-once delivery.** Publisher confirms give at-least-once;
  consumers must dedupe (DSN-017 / DSN-025 for the Kafka and
  forthcoming RabbitMQ idempotency stores).
- **Ordering across exchanges or across reconnects.** A single
  publish loop publishes in arrival order to its one exchange, but
  there's no cross-loop ordering and a reconnect may flush pending
  messages out of order.
- **Persistence across crashes before broker confirm.** A producer
  crash between `messages <- msg` and the broker Ack loses the
  message. Persistent producer state (outbox pattern) is not in
  scope.
- **Untraced retry after publish error.** When `pub.Publish` returns
  an error, the loop ends the producer span and re-queues the body
  for the next session — the retry attempt itself has no span.
  Full retry tracing would require restructuring the publish loop
  to track per-message confirm correlation, which is a follow-up.
