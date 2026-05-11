package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// runCatalogEnrichment exercises the DSN-018 outbound REST path: the
// app's GET /api/v1/inventory/{sku} handler calls the stub-catalog
// upstream and folds the description/category into the response. A
// pass means the response carries a non-empty catalog block whose
// description starts with "Catalog description for " (the stub's
// deterministic format).
func runCatalogEnrichment(ctx context.Context, cfg Config) (string, error) {
	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}

	sku := fmt.Sprintf("catalog-sku-%d", nowNanos())
	if err := createProduct(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("seed product: %w", err)
	}

	traceID, body, err := getInventory(ctx, cfg, tok, sku)
	if err != nil {
		return traceID, fmt.Errorf("read inventory: %w", err)
	}

	var resp struct {
		Sku     string `json:"sku"`
		Catalog *struct {
			Description string `json:"description"`
			Category    string `json:"category"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return traceID, fmt.Errorf("decode: %w", err)
	}
	if resp.Catalog == nil {
		return traceID, fmt.Errorf("response missing catalog block; outbound enrichment did not run (body=%s)", body)
	}
	if !strings.HasPrefix(resp.Catalog.Description, "Catalog description for ") {
		return traceID, fmt.Errorf("catalog.description=%q; expected stub upstream prefix", resp.Catalog.Description)
	}
	return traceID, nil
}

// getInventory wraps GET /api/v1/inventory/{sku} for the demo step.
// Returns the trace_id from the response headers (when present) so
// the summary table can link the call back to the collector, the raw
// body, and any error.
func getInventory(ctx context.Context, cfg Config, tok, sku string) (string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/api/v1/inventory/"+sku, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer res.Body.Close()
	traceID := traceFromHeader(res)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return traceID, nil, err
	}
	if res.StatusCode != http.StatusOK {
		return traceID, body, fmt.Errorf("GET inventory/%s: status %d body=%s", sku, res.StatusCode, body)
	}
	return traceID, body, nil
}
