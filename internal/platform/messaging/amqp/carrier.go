package amqp

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

// HeaderCarrier adapts an amqp.Table for use as an OTel
// propagation.TextMapCarrier. amqp091 stores header values as `any`;
// the propagator wants string round-trips, so non-string and missing
// values surface as empty strings (the propagator's documented
// "absent" semantics).
//
// Used by the producer (PublishInventory etc.) to inject W3C
// traceparent / tracestate / baggage onto outbound AMQP headers and
// by the consumer (handleProductMessage) to extract them on the way
// back in. There's no maintained `otelamqp091` upstream — this
// carrier is the ~20 lines that get us there.
type HeaderCarrier amqp.Table

func (c HeaderCarrier) Get(key string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return ""
}

func (c HeaderCarrier) Set(key, value string) { c[key] = value }

func (c HeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
