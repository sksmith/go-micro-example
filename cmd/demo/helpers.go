package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func ptrString(s string) *string { return &s }

func nowNanos() int64 { return time.Now().UnixNano() }

// restPut is the tiny PUT-JSON helper shared by demo steps. It bakes
// in the Bearer token, expects a 201 Created, and returns the body
// only on failure for diagnostics.
func restPut(ctx context.Context, cfg Config, tok, url string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("PUT %s: status %d body=%s", url, res.StatusCode, respBody)
	}
	return nil
}
