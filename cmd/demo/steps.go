package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	v1 "github.com/sksmith/go-micro-example/api/client/v1"
)

// Step is a single capability demonstration. Each capability ticket
// (DSN-016 through DSN-027) registers exactly one Step via Steps so
// the runner can iterate without further wiring. Run returns the
// trace_id captured from response headers (empty if none), plus any
// error.
type Step struct {
	Capability string
	Name       string
	Run        func(ctx context.Context, cfg Config) (traceID string, err error)
}

// Steps is the ordered list of demo steps the runner executes.
// New capability tickets append a step here.
var Steps = []Step{
	{
		Capability: "DSN-026",
		Name:       "REST create + read via generated client",
		Run:        runRESTRoundTrip,
	},
	{
		Capability: "DSN-016",
		Name:       "Kafka command → product_quantity_changed event",
		Run:        runKafkaRoundTrip,
	},
	{
		Capability: "DSN-017",
		Name:       "Duplicate Kafka command applied exactly once",
		Run:        runKafkaDuplicateReplay,
	},
}

// runRESTRoundTrip exchanges admin Basic credentials for a JWT,
// creates a unique product via the generated OpenAPI client, then
// reads it back. Proves the spec, the generated client, and the
// running app agree on the wire format. (DSN-026 acceptance.)
func runRESTRoundTrip(ctx context.Context, cfg Config) (string, error) {
	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}

	client, err := v1.NewClient(cfg.BaseURL, v1.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}))
	if err != nil {
		return "", fmt.Errorf("new client: %w", err)
	}

	sku := fmt.Sprintf("demo-sku-%d", nowNanos())
	// oapi-codegen's OpenAPI 3.1 union types marshal to "{}" through
	// the typed entrypoint (the MarshalJSON method lives on the
	// underlying type but doesn't transfer to the defined request
	// type — a known oapi-codegen 3.1 limitation). Marshal the
	// variant ourselves and use the WithBody entrypoint instead.
	createBody, err := json.Marshal(v1.ApiCreateProductRequest{
		Sku:  ptrString(sku),
		Upc:  ptrString("demo-upc"),
		Name: ptrString("Demo Widget"),
	})
	if err != nil {
		return "", fmt.Errorf("encode create body: %w", err)
	}
	createRes, err := client.PutApiV1InventoryWithBody(ctx, "application/json", bytes.NewReader(createBody))
	if err != nil {
		return "", fmt.Errorf("PUT inventory: %w", err)
	}
	defer createRes.Body.Close()
	traceID := traceFromHeader(createRes)
	if createRes.StatusCode != http.StatusCreated {
		return traceID, unexpectedStatus("PUT /api/v1/inventory", createRes)
	}

	getRes, err := client.GetApiV1InventorySku(ctx, sku)
	if err != nil {
		return traceID, fmt.Errorf("GET inventory/{sku}: %w", err)
	}
	defer getRes.Body.Close()
	if getRes.StatusCode != http.StatusOK {
		return traceID, unexpectedStatus("GET /api/v1/inventory/{sku}", getRes)
	}

	return traceID, nil
}

// fetchToken hits POST /auth/token with HTTP Basic so the demo's
// subsequent calls can present a Bearer JWT. The token endpoint is
// the only one that still accepts Basic (SEC-002c).
func fetchToken(ctx context.Context, cfg Config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/auth/token", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("token endpoint status %d: %s", res.StatusCode, body)
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return "", err
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned empty access_token")
	}
	return resp.AccessToken, nil
}

func unexpectedStatus(label string, res *http.Response) error {
	body, _ := io.ReadAll(res.Body)
	return fmt.Errorf("%s: status %d body=%s", label, res.StatusCode, body)
}

func traceFromHeader(res *http.Response) string {
	// otelchi (DSN-004) returns the W3C traceparent on responses when
	// tracing is on. Format: 00-<trace_id>-<span_id>-<flags>.
	if tp := res.Header.Get("Traceparent"); tp != "" {
		parts := splitOnce(tp, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return ""
}

func splitOnce(s, sep string) []string {
	out := make([]string, 0, 4)
	for {
		i := indexOf(s, sep)
		if i < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:i])
		s = s[i+len(sep):]
	}
}

func indexOf(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
