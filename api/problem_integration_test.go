package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

// TestProblemResponseConformsToRFC7807 covers the contract bits DSN-010
// hangs on:
//   - Content-Type is application/problem+json (RFC 7807 §3).
//   - type/title/status/instance fields are populated.
//   - 5xx responses do not leak wrapped error strings into detail.
func TestProblemResponseConformsToRFC7807(t *testing.T) {
	ts, mockInvSvc := setupInventoryTestServer()
	defer ts.Close()

	const secret = "raw-database-error-with-pii"
	mockInvSvc.GetAllProductInventoryFunc = func(ctx context.Context, limit, offset int) ([]inventory.ProductInventory, error) {
		return nil, errors.New(secret)
	}

	res, err := http.Get(ts.URL + "/?limit=10&offset=0")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status got=%d want=500", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("Content-Type got=%q want application/problem+json", got)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), secret) {
		t.Errorf("ISE body leaked underlying error: %s", body)
	}

	var p httpx.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if p.Type == "" {
		t.Errorf("type missing: %+v", p)
	}
	if p.Title == "" {
		t.Errorf("title missing: %+v", p)
	}
	if p.Status != http.StatusInternalServerError {
		t.Errorf("status got=%d want=500", p.Status)
	}
	if p.Instance == "" {
		t.Errorf("instance missing: %+v", p)
	}
}
