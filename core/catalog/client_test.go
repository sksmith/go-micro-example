package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/core/catalog"
	"github.com/sksmith/go-micro-example/core/observability"
)

func newClient(t *testing.T, server *httptest.Server, cfg catalog.Config) *catalog.HTTPClient {
	t.Helper()
	if cfg.BaseURL == "" {
		cfg.BaseURL = server.URL
	}
	c, err := catalog.NewHTTPClient(cfg)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return c
}

func TestLookupSuccessReturnsProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Request-Id"); got != "abc-123" {
			t.Errorf("X-Request-Id = %q, want %q", got, "abc-123")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"sku":"sku-1","description":"Widget","category":"tools"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv, catalog.Config{})
	ctx := observability.ContextWithRequestID(context.Background(), "abc-123")

	got, err := c.Lookup(ctx, "sku-1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Description != "Widget" || got.Category != "tools" || got.Sku != "sku-1" {
		t.Errorf("Lookup = %+v", got)
	}
}

func TestLookupNotFoundReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(t, srv, catalog.Config{})

	_, err := c.Lookup(context.Background(), "sku-1")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Lookup error = %v, want ErrNotFound", err)
	}
}

func TestLookupRetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"sku":"sku-1","description":"Widget"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv, catalog.Config{
		MaxAttempts: 3,
		BackoffBase: 1 * time.Millisecond,
	})

	got, err := c.Lookup(context.Background(), "sku-1")
	if err != nil {
		t.Fatalf("Lookup after retries: %v", err)
	}
	if got.Description != "Widget" {
		t.Errorf("Lookup = %+v", got)
	}
	if c := atomic.LoadInt32(&calls); c != 3 {
		t.Errorf("server saw %d calls, want 3", c)
	}
}

func TestLookupDoesNotRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newClient(t, srv, catalog.Config{
		MaxAttempts: 5,
		BackoffBase: 1 * time.Millisecond,
	})

	_, err := c.Lookup(context.Background(), "sku-1")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("400 should not surface as ErrNotFound")
	}
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Errorf("server saw %d calls, want 1 (no retry on 4xx)", c)
	}
}

func TestLookupHonorsTotalTimeout(t *testing.T) {
	// Per-attempt timeout (50ms) is below the server's deliberate
	// 300ms stall, so every attempt times out at the transport layer
	// and the total-deadline (150ms) trips before MaxAttempts is
	// exhausted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClient(t, srv, catalog.Config{
		Timeout:           150 * time.Millisecond,
		PerAttemptTimeout: 50 * time.Millisecond,
		MaxAttempts:       5,
		BackoffBase:       1 * time.Millisecond,
	})

	start := time.Now()
	_, err := c.Lookup(context.Background(), "sku-1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Lookup took %s; total-deadline (150ms) was not enforced", elapsed)
	}
}

func TestNewHTTPClientRequiresBaseURL(t *testing.T) {
	if _, err := catalog.NewHTTPClient(catalog.Config{}); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

func TestLookupRequiresSku(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newClient(t, srv, catalog.Config{})
	if _, err := c.Lookup(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty sku")
	}
}
