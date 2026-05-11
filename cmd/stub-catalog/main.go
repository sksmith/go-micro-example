// Command stub-catalog is the minimal upstream that DSN-018's
// outbound REST client demo calls. It serves GET /products/{sku} with
// a deterministic JSON body derived from the SKU. The shape mirrors
// core/catalog.Product on purpose — there's no schema sharing
// between the two, so reading them side-by-side is the only check
// against drift. Unknown SKUs return 404.
//
// The server is intentionally simple: no auth, no persistence, no
// extra deps. It exists to give the outbound-REST demo something
// real to hit inside docker-compose. A second route, /flaky/{sku},
// fails N times before succeeding, so the demo can also exercise
// the retry branch of the client.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type product struct {
	Sku         string `json:"sku"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

func main() {
	addr := os.Getenv("STUB_CATALOG_ADDR")
	if addr == "" {
		addr = ":9100"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/products/", handleProduct)
	mux.HandleFunc("/flaky/", flakyHandler(2))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	_, _ = fmt.Fprintf(os.Stdout, "stub-catalog listening on %s\n", addr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "stub-catalog: %v\n", err)
		os.Exit(1)
	}
}

func handleProduct(w http.ResponseWriter, r *http.Request) {
	sku := strings.TrimPrefix(r.URL.Path, "/products/")
	if sku == "" || strings.Contains(sku, "/") {
		http.Error(w, "bad sku", http.StatusBadRequest)
		return
	}
	// Reserve a "missing-*" prefix as a synthetic 404 so the demo
	// can assert the not-found path without needing a coordinated
	// pre-seed step.
	if strings.HasPrefix(sku, "missing-") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(product{
		Sku:         sku,
		Description: "Catalog description for " + sku,
		Category:    "demo",
	})
}

// flakyHandler returns 5xx for the first `failures` calls per sku,
// then succeeds. State is in-memory and process-lifetime.
func flakyHandler(failures int32) http.HandlerFunc {
	var counts sync.Map
	return func(w http.ResponseWriter, r *http.Request) {
		sku := strings.TrimPrefix(r.URL.Path, "/flaky/")
		if sku == "" {
			http.Error(w, "bad sku", http.StatusBadRequest)
			return
		}
		cur, _ := counts.LoadOrStore(sku, new(int32))
		n := atomic.AddInt32(cur.(*int32), 1)
		if n <= failures {
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(product{
			Sku:         sku,
			Description: "Flaky-but-recovered description for " + sku,
			Category:    "demo",
		})
	}
}
