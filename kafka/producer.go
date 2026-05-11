package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/events"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/propagation"
)

// Producer publishes domain events to Kafka. It wraps every payload
// in events.Envelope and injects the W3C traceparent header for
// downstream trace stitching.
type Producer struct {
	client *kgo.Client
	topic  string
}

// NewProducer builds a Kafka producer aimed at the given topic. The
// returned client is fully initialized; call Close on shutdown.
func NewProducer(brokers []string, topic string) (*Producer, error) {
	ensureMetrics()
	tracer := kotel.NewTracer(kotel.TracerProvider(nil))
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.AllowAutoTopicCreation(),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.WithHooks(kotel.NewKotel(kotel.WithTracer(tracer)).Hooks()...),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: client, topic: topic}, nil
}

// Close flushes pending writes and tears the client down.
func (p *Producer) Close() {
	if p == nil || p.client == nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.client.Flush(flushCtx); err != nil {
		log.Warn().Err(err).Msg("kafka producer flush on close")
	}
	p.client.Close()
}

// Publish wraps payload in an Envelope of the given event_type and
// writes it to the producer's topic synchronously. Returns an error
// only if envelope construction or the Kafka broker rejected the
// write — the caller may retry safely.
func (p *Producer) Publish(ctx context.Context, eventType string, payload any) error {
	env, err := events.NewEnvelope(uuid.NewString(), eventType, 1, time.Now(), payload)
	if err != nil {
		return fmt.Errorf("build envelope: %w", err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	rec := &kgo.Record{
		Topic:   p.topic,
		Value:   body,
		Headers: producerHeaders(ctx, env.EventID),
	}
	if err := p.client.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return fmt.Errorf("produce: %w", err)
	}
	producedCounter.Inc()
	log.Ctx(ctx).Debug().Str("topic", p.topic).Str("event_id", env.EventID).Str("event_type", eventType).Msg("kafka publish")
	return nil
}

// producerHeaders writes the event_id and the W3C traceparent for the
// current span (if any) onto Kafka headers so consumers can correlate
// without parsing the envelope first.
func producerHeaders(ctx context.Context, eventID string) []kgo.RecordHeader {
	hs := []kgo.RecordHeader{{Key: HeaderEventID, Value: []byte(eventID)}}

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	if tp := carrier.Get("traceparent"); tp != "" {
		hs = append(hs, kgo.RecordHeader{Key: HeaderTraceparent, Value: []byte(tp)})
	}
	return hs
}
