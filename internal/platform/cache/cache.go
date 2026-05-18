// Package cache is the data-layer caching primitive introduced by
// DSN-020. The package's job is small on purpose: a single Cache
// interface for byte-payload Get/Set/Delete, plus two helpers
// (cache.Get[T] / cache.Set[T]) that JSON-encode arbitrary values
// over the interface. Callers compose these into cache-aside reads at
// the service layer — the cache deliberately doesn't wrap handlers
// (the ticket spells that out) so non-HTTP consumers (queue-driven
// flows, future gRPC) benefit too.
//
// Two implementations ship here:
//
//   - RedisCache is the production impl, backed by redis/go-redis/v9.
//   - MemoryCache is an in-process map used by tests. Behaviour
//     matches Redis closely enough for the contract tests in
//     cache_test.go — TTL is honoured, Delete and missing keys
//     return the right signals.
//
// The interface returns (value, found, error) on Get instead of
// folding the miss into a sentinel error: the cache-miss path is
// hot, and callers shouldn't need errors.Is() to detect it.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Cache is the byte-level interface every implementation satisfies.
// Keep the surface small — anything fancier (Incr, MGet, scripts)
// belongs in a dedicated package built alongside the use-case that
// needs it.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// ErrCacheUnavailable wraps the underlying error when an impl can't
// reach its backing store. Callers use errors.Is on this sentinel to
// distinguish a transient outage (degrade open) from a true protocol
// error (which is still rare enough to be worth a separate log).
var ErrCacheUnavailable = errors.New("cache: backing store unavailable")

// Get is the typed read helper. It pulls the raw bytes via c, then
// json.Unmarshals into T. A miss returns the zero value of T,
// found=false, err=nil. A decode error returns found=false and the
// error so the caller can decide whether to log-and-fall-back to the
// authoritative source.
func Get[T any](ctx context.Context, c Cache, key string) (T, bool, error) {
	var zero T
	raw, ok, err := c.Get(ctx, key)
	if err != nil {
		hitsByPrefix.WithLabelValues(prefix(key), "error").Inc()
		return zero, false, err
	}
	if !ok {
		hitsByPrefix.WithLabelValues(prefix(key), "miss").Inc()
		return zero, false, nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		// A bad value in the cache shouldn't poison reads. Drop it,
		// surface the decode error, and let the caller refetch.
		_ = c.Delete(ctx, key)
		hitsByPrefix.WithLabelValues(prefix(key), "error").Inc()
		return zero, false, err
	}
	hitsByPrefix.WithLabelValues(prefix(key), "hit").Inc()
	return v, true, nil
}

// Set is the typed write helper. It json.Marshals and stores under
// key with the given TTL. Returns the encoding/IO error verbatim;
// callers typically log and continue — a failure to populate the
// cache is never a reason to fail the request that produced the
// value.
func Set[T any](ctx context.Context, c Cache, key string, value T, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, raw, ttl)
}

var (
	metricsOnce  sync.Once
	hitsByPrefix *prometheus.CounterVec
)

func ensureMetrics() {
	metricsOnce.Do(func() {
		hitsByPrefix = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_requests_total",
			Help: "Cache Get outcomes, labelled by key prefix and outcome (hit|miss|error).",
		}, []string{"prefix", "outcome"})
		prometheus.MustRegister(hitsByPrefix)
	})
}

// prefix returns the substring of key up to the first ':' so metrics
// stay bounded — cardinality blows up if every SKU becomes a label.
// "inv:product:abc" → "inv:product". Keys without a ':' fall back to
// the literal key (used in tests).
func prefix(key string) string {
	for i, r := range key {
		if r == ':' {
			// Take everything up to the SECOND colon so the namespace
			// (e.g. "inv:product") survives but the unique tail is
			// stripped. Falls through to single-colon behaviour when
			// there isn't a second one.
			for j := i + 1; j < len(key); j++ {
				if key[j] == ':' {
					return key[:j]
				}
			}
			return key[:i]
		}
	}
	return key
}

func init() { ensureMetrics() }
