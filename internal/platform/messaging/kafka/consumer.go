package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/propagation"
)

// Handler is the application-level entrypoint for an incoming command
// envelope. Returning nil commits the offset; returning a non-nil
// error triggers the retry/DLT path.
type Handler interface {
	Handle(ctx context.Context, env events.Envelope) error
}

// Consumer joins a named group and dispatches messages to Handler.
// Offsets are committed only after Handler returns nil — at-least-once
// delivery. DSN-017 wraps Handler with a dedupe table to make it
// exactly-once at the side-effect layer.
type Consumer struct {
	client   *kgo.Client
	dltProd  *kgo.Client
	handler  Handler
	topic    string
	dltTopic string
	group    string

	maxRetries int
	retryBase  time.Duration
}

// ConsumerConfig collects the wiring options. Producer/Handler/etc.
// are all required; the retry knobs default to 3 retries / 200ms base.
type ConsumerConfig struct {
	Brokers    []string
	Topic      string
	DLTTopic   string
	Group      string
	Handler    Handler
	MaxRetries int
	RetryBase  time.Duration
}

// NewConsumer builds the consumer client and a small dedicated DLT
// producer. The consumer is not started; call Run to begin polling.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Handler == nil {
		return nil, errors.New("kafka consumer: handler is required")
	}
	ensureMetrics()

	tracer := kotel.NewTracer(kotel.TracerProvider(nil))
	hooks := kotel.NewKotel(kotel.WithTracer(tracer)).Hooks()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topic),
		// We commit manually after the handler succeeds so a crash
		// mid-handle does not lose the message.
		kgo.DisableAutoCommit(),
		kgo.WithHooks(hooks...),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}

	dltProd, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.DefaultProduceTopic(cfg.DLTTopic),
		kgo.AllowAutoTopicCreation(),
		kgo.WithHooks(hooks...),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka DLT producer: %w", err)
	}

	max := cfg.MaxRetries
	if max <= 0 {
		max = 3
	}
	base := cfg.RetryBase
	if base <= 0 {
		base = 200 * time.Millisecond
	}

	return &Consumer{
		client:     client,
		dltProd:    dltProd,
		handler:    cfg.Handler,
		topic:      cfg.Topic,
		dltTopic:   cfg.DLTTopic,
		group:      cfg.Group,
		maxRetries: max,
		retryBase:  base,
	}, nil
}

// Close stops the consumer and tears the clients down.
func (c *Consumer) Close() {
	if c == nil {
		return
	}
	if c.client != nil {
		c.client.Close()
	}
	if c.dltProd != nil {
		c.dltProd.Close()
	}
}

// Run polls the broker and dispatches messages until ctx is canceled.
// Returns nil on graceful shutdown.
func (c *Consumer) Run(ctx context.Context) error {
	log.Info().Str("topic", c.topic).Str("group", c.group).Msg("kafka consumer running")
	for {
		fetches := c.client.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fe := range errs {
				if errors.Is(fe.Err, context.Canceled) {
					return nil
				}
				log.Warn().Err(fe.Err).Str("topic", fe.Topic).Int32("partition", fe.Partition).Msg("kafka fetch error")
			}
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			c.dispatch(ctx, rec)
		})

		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			log.Warn().Err(err).Msg("kafka commit failed; will retry on next poll")
		}

		if ctx.Err() != nil {
			return nil
		}
	}
}

// dispatch runs the handler with bounded retries; on exhaustion the
// original record is republished to the DLT and the offset is allowed
// to commit (the consumer must not get stuck).
func (c *Consumer) dispatch(parent context.Context, rec *kgo.Record) {
	ctx := contextFromHeaders(parent, rec)

	env, err := events.Validate(rec.Value)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("invalid kafka envelope; routing to DLT")
		consumeErrors.Inc()
		c.toDLT(parent, rec, fmt.Sprintf("invalid envelope: %v", err))
		return
	}

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		err := c.handler.Handle(ctx, env)
		if err == nil {
			consumedCounter.Inc()
			return
		}
		consumeErrors.Inc()
		log.Ctx(ctx).Warn().Err(err).Int("attempt", attempt+1).Int("max", c.maxRetries+1).Str("event_id", env.EventID).Msg("kafka handler failed")
		if attempt == c.maxRetries {
			c.toDLT(parent, rec, err.Error())
			return
		}
		backoff := c.retryBase * time.Duration(1<<attempt)
		select {
		case <-parent.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (c *Consumer) toDLT(ctx context.Context, rec *kgo.Record, reason string) {
	hs := append([]kgo.RecordHeader{}, rec.Headers...)
	hs = append(hs, kgo.RecordHeader{Key: "x-dlt-reason", Value: []byte(reason)})
	dltRec := &kgo.Record{Topic: c.dltTopic, Key: rec.Key, Value: rec.Value, Headers: hs}
	if err := c.dltProd.ProduceSync(ctx, dltRec).FirstErr(); err != nil {
		log.Ctx(ctx).Error().Err(err).Str("dlt", c.dltTopic).Msg("failed to publish to DLT")
		return
	}
	dltSent.Inc()
	log.Ctx(ctx).Info().Str("dlt", c.dltTopic).Str("reason", reason).Msg("message routed to DLT")
}

func contextFromHeaders(parent context.Context, rec *kgo.Record) context.Context {
	carrier := propagation.MapCarrier{}
	for _, h := range rec.Headers {
		if h.Key == HeaderTraceparent {
			carrier.Set("traceparent", string(h.Value))
		}
	}
	if carrier.Get("traceparent") == "" {
		return parent
	}
	return propagation.TraceContext{}.Extract(parent, carrier)
}
