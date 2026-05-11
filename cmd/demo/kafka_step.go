package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sksmith/go-micro-example/events"
	"github.com/twmb/franz-go/pkg/kgo"
)

// runKafkaRoundTrip publishes an inventory.record_production v1
// command to the inventory.commands.v1 topic and waits for the
// resulting inventory.product_quantity_changed event on the events
// topic. Proves the Kafka producer and consumer paths are wired
// correctly end-to-end (DSN-016).
//
// Skipped when DEMO_KAFKA_BROKERS is empty so the demo still runs in
// environments where Kafka isn't part of the stack.
func runKafkaRoundTrip(ctx context.Context, cfg Config) (string, error) {
	if cfg.KafkaBrokers == "" {
		return "", fmt.Errorf("DEMO_KAFKA_BROKERS unset; skipping Kafka step")
	}
	brokers := strings.Split(cfg.KafkaBrokers, ",")

	// 1. Create a unique SKU so the test is isolated, via REST.
	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	sku := fmt.Sprintf("kafka-sku-%d", nowNanos())
	if err := createProduct(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("seed product: %w", err)
	}

	// 2. Set up a watcher on the events topic. Reading from the
	// start of the topic and filtering by SKU is more reliable in
	// the demo than racing the producer with an AtEnd cursor —
	// the topic is small and the SKU is unique per run.
	watcher, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(cfg.KafkaEventsTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return "", fmt.Errorf("watcher client: %w", err)
	}
	defer watcher.Close()

	// 3. Publish the command.
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return "", fmt.Errorf("producer client: %w", err)
	}
	defer producer.Close()

	const quantity = 7
	env, err := events.NewEnvelope(uuid.NewString(), events.TypeRecordProduction, 1, time.Now(),
		map[string]any{"sku": sku, "requestId": uuid.NewString(), "quantity": quantity},
	)
	if err != nil {
		return "", fmt.Errorf("envelope: %w", err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal command: %w", err)
	}
	if err := producer.ProduceSync(ctx, &kgo.Record{
		Topic: cfg.KafkaCommandsTopic,
		Value: body,
	}).FirstErr(); err != nil {
		return "", fmt.Errorf("produce command: %w", err)
	}

	// 4. Wait for the matching product_quantity_changed event.
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		fetches := watcher.PollFetches(waitCtx)
		if waitCtx.Err() != nil {
			return env.EventID, fmt.Errorf("timed out waiting for product_quantity_changed event for sku=%s", sku)
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return env.EventID, fmt.Errorf("watcher fetch: %w", errs[0].Err)
		}
		var matched bool
		fetches.EachRecord(func(rec *kgo.Record) {
			if matched {
				return
			}
			obs, err := events.Validate(rec.Value)
			if err != nil {
				return
			}
			if obs.EventType != events.TypeProductQuantityChanged {
				return
			}
			var payload struct {
				Sku       string `json:"sku"`
				Available int64  `json:"available"`
			}
			if err := json.Unmarshal(obs.Payload, &payload); err != nil {
				return
			}
			if payload.Sku == sku {
				matched = true
			}
		})
		if matched {
			return env.EventID, nil
		}
	}
}

// createProduct is the small REST helper the Kafka step uses to seed
// a SKU before publishing its command. UPC is derived from the SKU
// so concurrent demo steps don't collide on the products_upc_key
// unique constraint.
func createProduct(ctx context.Context, cfg Config, tok, sku string) error {
	type product struct {
		Sku  string `json:"sku"`
		Upc  string `json:"upc"`
		Name string `json:"name"`
	}
	return restPut(ctx, cfg, tok, cfg.BaseURL+"/api/v1/inventory", product{
		Sku:  sku,
		Upc:  "upc-" + sku,
		Name: "demo SKU " + sku,
	})
}
