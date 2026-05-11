// Package rest is the HTTP-side half of the idempotency story
// (DSN-019). Where the parent idempotency package guards Kafka
// handlers via an event-id dedupe row, this subpackage caches whole
// REST responses keyed on the client-supplied Idempotency-Key header
// so a safe retry replays the original status, body, and selected
// headers byte-for-byte instead of running the handler twice.
//
// The Store interface is the seam between the middleware and a
// backing implementation. An in-memory implementation ships here so
// the middleware works out of the box and tests can run without
// network dependencies; DSN-021 will plug in Redis as the production
// store. The two implementations must agree on the contract spelled
// out below — the middleware's correctness depends on it.
//
// Store contract:
//
//   - Lookup(ctx, key) returns the cached entry under key, a boolean
//     reporting whether one was found, and an error only on I/O
//     failure (a miss is not an error).
//   - Save(ctx, key, entry, ttl) atomically records entry under key
//     with the given TTL. Implementations must overwrite an existing
//     entry — the middleware only calls Save when it has already
//     established the key wasn't present (or had expired).
//   - Entries expire after ttl elapses since Save. After expiry,
//     Lookup returns found=false.
//   - Concurrency: two simultaneous Save calls for the same key are
//     allowed; the last write wins. The middleware tolerates this
//     because the cached response is deterministic up to the
//     (key, body-hash) pair — both writers serialized the same
//     handler output.
package rest

import (
	"context"
	"sync"
	"time"
)

// Entry is the cached representation of a recorded response. The
// middleware records BodyHash so a retry that reuses the key with a
// different request body can be rejected with 409 — the Stripe
// idempotency contract DSN-019 references.
//
// Headers is a flat list rather than http.Header so the entry can be
// serialised cheaply (in-memory uses a copy; a future Redis store
// would JSON-encode it). The middleware records only a curated
// allow-list (Content-Type, plus the application's correlation
// headers); cookies, auth, and Set-Cookie are intentionally excluded.
type Entry struct {
	Status   int        `json:"status"`
	BodyHash string     `json:"bodyHash"`
	Body     []byte     `json:"body"`
	Headers  []HeaderKV `json:"headers"`
	SavedAt  time.Time  `json:"savedAt"`
}

// HeaderKV is one recorded response-header entry. Slice-of-struct
// rather than map[string][]string because the middleware records a
// fixed, small set; the linear scan on replay is cheaper than the
// allocation overhead of a map.
type HeaderKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Store is the seam tests mock at. Production code receives a
// *MemoryStore or (post-DSN-021) a Redis-backed implementation; the
// middleware only sees this surface.
type Store interface {
	Lookup(ctx context.Context, key string) (Entry, bool, error)
	Save(ctx context.Context, key string, entry Entry, ttl time.Duration) error
}

// MemoryStore is an in-process Store. Safe for concurrent use.
// Entries expire lazily — a Lookup that hits an expired entry returns
// found=false and deletes the row. A background goroutine isn't
// started here on purpose: the demo and tests run for seconds, not
// hours, and the lazy sweep is enough.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

type memoryEntry struct {
	entry    Entry
	expireAt time.Time
}

// NewMemoryStore returns a ready-to-use in-memory Store. The caller
// owns its lifetime; nothing here needs explicit shutdown.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]memoryEntry)}
}

// Lookup returns the entry under key, dropping it if it has expired.
func (m *MemoryStore) Lookup(_ context.Context, key string) (Entry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.entries[key]
	if !ok {
		return Entry{}, false, nil
	}
	if time.Now().After(row.expireAt) {
		delete(m.entries, key)
		return Entry{}, false, nil
	}
	return row.entry, true, nil
}

// Save writes entry under key with the given TTL, overwriting any
// existing value (the middleware only reaches this path after a
// Lookup miss).
func (m *MemoryStore) Save(_ context.Context, key string, entry Entry, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = memoryEntry{
		entry:    entry,
		expireAt: time.Now().Add(ttl),
	}
	return nil
}

// Size reports the current number of stored entries. Exposed for
// tests; production code should rely on cache metrics instead.
func (m *MemoryStore) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
