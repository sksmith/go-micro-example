// Package kafka is the Kafka producer/consumer pair from DSN-016.
//
// Producer (Producer): wraps a payload in events.Envelope and writes
// it to a configured topic. Each produce records a Prometheus counter
// and emits an OpenTelemetry span via kotel so traces span the wire
// boundary.
//
// Consumer (Consumer): joins a named group, validates each message
// against the events schema registry, dispatches to the supplied
// Handler, and commits the offset only after the handler returns nil.
// Transient handler errors are retried in-memory with exponential
// backoff; once the retry budget is exhausted the original message is
// republished to a dead-letter topic and the offset is committed so
// the consumer doesn't get stuck.
//
// Idempotency is NOT provided here — DSN-017 wraps Handler with a
// dedupe table. Until then, this consumer offers at-least-once
// semantics like any Kafka consumer.
package kafka

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// HeaderTraceparent carries the W3C traceparent string so consumers
// can stitch their spans back to the producer's trace.
const HeaderTraceparent = "traceparent"

// HeaderEventID surfaces the envelope's event_id in Kafka headers so
// operators can locate a message by ID without decoding the body.
// DSN-017 will key idempotency off this same value.
const HeaderEventID = "event_id"

var (
	metricsOnce sync.Once

	producedCounter prometheus.Counter
	consumedCounter prometheus.Counter
	consumeErrors   prometheus.Counter
	dltSent         prometheus.Counter
)

func initMetrics() {
	producedCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kafka_events_produced_total",
		Help: "Total Kafka messages produced.",
	})
	consumedCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kafka_events_consumed_total",
		Help: "Total Kafka messages successfully handled.",
	})
	consumeErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kafka_events_failed_total",
		Help: "Total handler errors (each retry counts; see kafka_events_dlt_total for terminal failures).",
	})
	dltSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kafka_events_dlt_total",
		Help: "Total messages republished to the dead-letter topic after exhausting their retry budget.",
	})
	prometheus.MustRegister(producedCounter, consumedCounter, consumeErrors, dltSent)
}

// ensureMetrics registers the counters once, idempotently.
func ensureMetrics() { metricsOnce.Do(initMetrics) }
