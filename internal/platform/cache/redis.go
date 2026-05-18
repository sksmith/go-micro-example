package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient is the slice of redis.Cmdable RedisCache actually uses.
// Narrowing the surface keeps tests fakeable — redis.Cmdable itself
// is hundreds of methods. A *redis.Client satisfies this interface
// directly so production wiring needs no adapter.
type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Ping(ctx context.Context) *redis.StatusCmd
}

// RedisCache is the production Cache impl. It wraps a RedisClient and
// translates Redis-specific errors into the package's interface
// contract: a key that doesn't exist returns found=false, err=nil; a
// network/protocol failure returns err wrapping ErrCacheUnavailable
// so callers can degrade open via errors.Is.
//
// The client itself is reused, not owned — main constructs one and
// passes it here, and (once DSN-021 lands) the same client backs the
// rate limiter, idempotency store, and user cache. Closing is the
// caller's job.
type RedisCache struct {
	client RedisClient
}

// NewRedisCache wraps an existing RedisClient (typically a
// *redis.Client). Construction is trivial because go-redis
// lazy-dials; the first Get/Set surfaces any real connectivity
// failure.
func NewRedisCache(client RedisClient) *RedisCache {
	return &RedisCache{client: client}
}

func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: get %s: %w", ErrCacheUnavailable, key, err)
	}
	return val, true, nil
}

func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("%w: set %s: %w", ErrCacheUnavailable, key, err)
	}
	return nil
}

func (r *RedisCache) Delete(ctx context.Context, key string) error {
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("%w: del %s: %w", ErrCacheUnavailable, key, err)
	}
	return nil
}

// Ping verifies connectivity. Used by the /ready probe (DSN-002
// follow-up) so a cold-started Redis container doesn't pass health
// checks before it's actually serving.
func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
