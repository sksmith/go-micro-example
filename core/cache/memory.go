package cache

import (
	"context"
	"sync"
	"time"
)

// MemoryCache is an in-process Cache used by tests and by callers
// that want the cache-aside structure without a Redis dependency
// (e.g. demo runs without DSN-020's Redis service). Safe for
// concurrent use. Entries expire lazily — a Get that hits an expired
// entry returns found=false and evicts the row.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

type memoryEntry struct {
	value    []byte
	expireAt time.Time
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{entries: make(map[string]memoryEntry)}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !e.expireAt.IsZero() && time.Now().After(e.expireAt) {
		delete(m.entries, key)
		return nil, false, nil
	}
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true, nil
}

func (m *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp := time.Time{}
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	m.entries[key] = memoryEntry{value: stored, expireAt: exp}
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return nil
}

// Size reports the number of stored entries. Exposed for tests only.
func (m *MemoryCache) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
