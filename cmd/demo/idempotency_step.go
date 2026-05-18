package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/twmb/franz-go/pkg/kgo"
)

// runKafkaDuplicateReplay publishes the SAME envelope (identical
// event_id) twice with two different inner requestIds and asserts the
// production was applied exactly once. The differing requestIds rule
// out the inventory service's own request-id-based guard, leaving
// only DSN-017's processed_events dedupe as the responsible layer.
//
// A successful run means: available-after == quantity, NOT 2*quantity.
func runKafkaDuplicateReplay(ctx context.Context, cfg Config) (string, error) {
	if cfg.KafkaBrokers == "" {
		return "", fmt.Errorf("DEMO_KAFKA_BROKERS unset; skipping idempotency step")
	}
	brokers := strings.Split(cfg.KafkaBrokers, ",")

	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}

	sku := fmt.Sprintf("dedupe-sku-%d", nowNanos())
	if err := createProduct(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("seed product: %w", err)
	}

	producer, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return "", fmt.Errorf("producer client: %w", err)
	}
	defer producer.Close()

	const quantity = int64(5)
	sharedEventID := uuid.NewString()

	publishCommand := func(requestID string) error {
		env, err := events.NewEnvelope(sharedEventID, events.TypeRecordProduction, 1, time.Now(),
			map[string]any{"sku": sku, "requestId": requestID, "quantity": quantity},
		)
		if err != nil {
			return err
		}
		body, err := json.Marshal(env)
		if err != nil {
			return err
		}
		return producer.ProduceSync(ctx, &kgo.Record{
			Topic: cfg.KafkaCommandsTopic,
			Value: body,
		}).FirstErr()
	}

	if err := publishCommand(uuid.NewString()); err != nil {
		return sharedEventID, fmt.Errorf("first publish: %w", err)
	}
	if err := publishCommand(uuid.NewString()); err != nil {
		return sharedEventID, fmt.Errorf("duplicate publish: %w", err)
	}

	// Give the consumer time to process both deliveries.
	deadline := time.Now().Add(15 * time.Second)
	for {
		avail, err := getAvailable(ctx, cfg, tok, sku)
		if err != nil {
			return sharedEventID, fmt.Errorf("read inventory: %w", err)
		}
		if avail == quantity {
			return sharedEventID, nil
		}
		if avail > quantity {
			return sharedEventID, fmt.Errorf("duplicate was applied: available=%d want=%d (idempotency wrapper let a redelivery through)", avail, quantity)
		}
		if time.Now().After(deadline) {
			return sharedEventID, fmt.Errorf("timed out waiting for production to apply (available=%d want=%d)", avail, quantity)
		}
		select {
		case <-ctx.Done():
			return sharedEventID, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// getAvailable reads the current available count for a SKU via the
// REST API. Tiny enough to keep here rather than in the generated
// client until DSN-027 grows the demo's UI surface.
func getAvailable(ctx context.Context, cfg Config, tok, sku string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/api/v1/inventory/"+sku, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return 0, fmt.Errorf("GET inventory/%s: status %d body=%s", sku, res.StatusCode, body)
	}
	var resp struct {
		Available int64 `json:"available"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return 0, err
	}
	return resp.Available, nil
}
