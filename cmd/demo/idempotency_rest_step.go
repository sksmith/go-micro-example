package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// runRESTIdempotency exercises DSN-019. The step:
//  1. Creates a fresh SKU via REST.
//  2. Sends PUT /productionEvent for that SKU with a unique
//     Idempotency-Key and a fresh inner request_id, recording the
//     response.
//  3. Sends the SAME request body again with the SAME
//     Idempotency-Key, asserts the response replays byte-for-byte
//     (Idempotent-Replay: true header present).
//  4. Reads the inventory and asserts the available count equals the
//     production quantity, not 2× the quantity — the retry must not
//     have applied a second production event.
//  5. Sends the same key with a DIFFERENT body and asserts 409 Conflict.
//
// Two layers prevent double-apply: the middleware's response cache
// AND the inventory service's request_id guard. Step 5 isolates the
// middleware (a different request_id would let the service through;
// the middleware doesn't care about request_id).
func runRESTIdempotency(ctx context.Context, cfg Config) (string, error) {
	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}

	sku := fmt.Sprintf("rest-idem-sku-%d", nowNanos())
	if err := createProduct(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("seed product: %w", err)
	}

	idemKey := uuid.NewString()
	requestID := uuid.NewString()
	const quantity = int64(5)

	body, err := json.Marshal(map[string]any{
		"requestId": requestID,
		"quantity":  quantity,
	})
	if err != nil {
		return idemKey, fmt.Errorf("marshal body: %w", err)
	}

	// First call: handler runs, response is recorded.
	first, _, err := putProduction(ctx, cfg, tok, sku, idemKey, body)
	if err != nil {
		return idemKey, fmt.Errorf("first PUT: %w", err)
	}
	if first.StatusCode != http.StatusCreated {
		return idemKey, fmt.Errorf("first PUT status=%d, want 201", first.StatusCode)
	}
	if got := first.Header.Get("Idempotent-Replay"); got != "" {
		return idemKey, fmt.Errorf("first response carried Idempotent-Replay=%q; should only appear on replays", got)
	}
	_ = first.Body.Close()

	// Second call: same key, same body — should replay byte-for-byte.
	second, _, err := putProduction(ctx, cfg, tok, sku, idemKey, body)
	if err != nil {
		return idemKey, fmt.Errorf("second PUT: %w", err)
	}
	if second.StatusCode != http.StatusCreated {
		return idemKey, fmt.Errorf("second PUT status=%d, want 201 (replay)", second.StatusCode)
	}
	if got := second.Header.Get("Idempotent-Replay"); got != "true" {
		return idemKey, fmt.Errorf("second response missing Idempotent-Replay=true; middleware did not replay (got %q)", got)
	}
	_ = second.Body.Close()

	// Verify only ONE production was applied.
	avail, err := getAvailable(ctx, cfg, tok, sku)
	if err != nil {
		return idemKey, fmt.Errorf("read inventory: %w", err)
	}
	if avail != quantity {
		return idemKey, fmt.Errorf("available=%d, want %d (retry double-applied)", avail, quantity)
	}

	// Same key, different body → 409.
	differentBody, _ := json.Marshal(map[string]any{
		"requestId": uuid.NewString(),
		"quantity":  quantity + 1,
	})
	conflict, _, err := putProduction(ctx, cfg, tok, sku, idemKey, differentBody)
	if err != nil {
		return idemKey, fmt.Errorf("third PUT: %w", err)
	}
	if conflict.StatusCode != http.StatusConflict {
		return idemKey, fmt.Errorf("third PUT status=%d, want 409 (key reuse with different body)", conflict.StatusCode)
	}
	_ = conflict.Body.Close()
	return idemKey, nil
}

// putProduction sends PUT /api/v1/inventory/{sku}/productionEvent
// with the supplied Idempotency-Key header and returns the response.
// The body is left to the caller to close.
func putProduction(ctx context.Context, cfg Config, tok, sku, idemKey string, body []byte) (*http.Response, []byte, error) {
	url := cfg.BaseURL + "/api/v1/inventory/" + sku + "/productionEvent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Idempotency-Key", idemKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	// Read body for diagnostics, then re-attach so the caller can
	// still inspect/close it.
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return res, raw, nil
}
