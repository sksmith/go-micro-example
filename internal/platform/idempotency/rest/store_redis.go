package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is the production Store impl (DSN-021a) backing the
// Idempotency-Key middleware with a Redis-resident JSON blob per key.
// JSON keeps entries inspectable from redis-cli — a recurring need
// when diagnosing a "why did this retry get 409" report. Redis key
// TTL handles expiry so the middleware doesn't have to sweep.
//
// The Redis client is reused from the shared *redis.Client built in
// cmd/main; DSN-020 already wired the same client into the inventory
// cache. RedisStore narrows the surface to the RedisClient
// interface declared by core/cache so tests can fake it without
// pulling all 200+ methods of redis.Cmdable.
type RedisStore struct {
	client redisClient
	prefix string
}

// redisClient is the slice of redis.Cmdable RedisStore actually
// uses. Mirrors core/cache.RedisClient (deliberately duplicated to
// avoid an import cycle — neither package owns the other).
type redisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// NewRedisStore wraps an existing Redis client. The key prefix is
// fixed at "idem:rest:" so the store is namespaced from the
// inventory cache and any future Redis users.
func NewRedisStore(client redisClient) *RedisStore {
	return &RedisStore{client: client, prefix: "idem:rest:"}
}

func (r *RedisStore) Lookup(ctx context.Context, key string) (Entry, bool, error) {
	raw, err := r.client.Get(ctx, r.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("idempotency redis get: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		// Bad value in Redis — treat as a miss so the middleware
		// reruns the handler. The next Save will overwrite the bad
		// row. Returning the error too gives operators visibility.
		return Entry{}, false, fmt.Errorf("idempotency redis decode: %w", err)
	}
	return entry, true, nil
}

func (r *RedisStore) Save(ctx context.Context, key string, entry Entry, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("idempotency redis encode: %w", err)
	}
	if err := r.client.Set(ctx, r.prefix+key, raw, ttl).Err(); err != nil {
		return fmt.Errorf("idempotency redis set: %w", err)
	}
	return nil
}
