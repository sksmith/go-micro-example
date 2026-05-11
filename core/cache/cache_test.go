package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sksmith/go-micro-example/core/cache"
)

type sampleValue struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestMemoryCacheHitMissDelete(t *testing.T) {
	c := cache.NewMemoryCache()
	ctx := context.Background()

	// Miss: empty cache.
	got, ok, err := c.Get(ctx, "k")
	if err != nil || ok || got != nil {
		t.Fatalf("empty Get: got=%v ok=%v err=%v", got, ok, err)
	}

	// Set then hit.
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err = c.Get(ctx, "k")
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("hit Get: got=%q ok=%v err=%v", got, ok, err)
	}

	// Delete.
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("Get returned hit after Delete")
	}
}

func TestMemoryCacheTTLExpiresEntry(t *testing.T) {
	c := cache.NewMemoryCache()
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 10*time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	_, ok, _ := c.Get(ctx, "k")
	if ok {
		t.Error("expected expired entry to miss")
	}
	if c.Size() != 0 {
		t.Errorf("expired entry not evicted on read; size=%d", c.Size())
	}
}

func TestTypedGetSetRoundtrip(t *testing.T) {
	c := cache.NewMemoryCache()
	ctx := context.Background()

	want := sampleValue{Name: "Widget", N: 42}
	if err := cache.Set(ctx, c, "inv:product:abc", want, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := cache.Get[sampleValue](ctx, c, "inv:product:abc")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("got=%+v want=%+v", got, want)
	}
}

func TestTypedGetMissReturnsZeroValue(t *testing.T) {
	c := cache.NewMemoryCache()
	got, ok, err := cache.Get[sampleValue](context.Background(), c, "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected miss")
	}
	if (got != sampleValue{}) {
		t.Errorf("Get on miss returned non-zero: %+v", got)
	}
}

func TestTypedGetCorruptCacheValueDeletesAndReturnsError(t *testing.T) {
	c := cache.NewMemoryCache()
	ctx := context.Background()
	// Plant a value that can't decode into sampleValue.
	_ = c.Set(ctx, "k", []byte("not-json"), time.Minute)
	_, ok, err := cache.Get[sampleValue](ctx, c, "k")
	if ok {
		t.Error("expected miss on corrupt value")
	}
	if err == nil {
		t.Error("expected decode error to surface")
	}
	// And the corrupt row should be gone so the next reader doesn't
	// hit the same failure.
	if _, hit, _ := c.Get(ctx, "k"); hit {
		t.Error("corrupt value should have been deleted")
	}
}

func TestRedisCacheDegradesOpenOnUnavailability(t *testing.T) {
	// failingClient returns transport errors on every call. The
	// RedisCache wrapper should wrap those with ErrCacheUnavailable
	// so callers can distinguish protocol failure from miss.
	rc := cache.NewRedisCache(&failingClient{err: errors.New("connection refused")})

	_, ok, err := rc.Get(context.Background(), "k")
	if ok {
		t.Error("expected ok=false on transport failure")
	}
	if !errors.Is(err, cache.ErrCacheUnavailable) {
		t.Errorf("Get error chain missing ErrCacheUnavailable: %v", err)
	}

	if err := rc.Set(context.Background(), "k", []byte("v"), time.Minute); !errors.Is(err, cache.ErrCacheUnavailable) {
		t.Errorf("Set error chain missing ErrCacheUnavailable: %v", err)
	}
	if err := rc.Delete(context.Background(), "k"); !errors.Is(err, cache.ErrCacheUnavailable) {
		t.Errorf("Delete error chain missing ErrCacheUnavailable: %v", err)
	}
}

func TestRedisCacheNilSentinelIsMissNotError(t *testing.T) {
	// redis.Nil is the library's "key not found" signal. The wrapper
	// must translate it to ok=false, err=nil so callers don't see a
	// false-positive cache outage when the key simply isn't there.
	rc := cache.NewRedisCache(&failingClient{err: redis.Nil})
	_, ok, err := rc.Get(context.Background(), "k")
	if ok || err != nil {
		t.Errorf("expected ok=false, err=nil for redis.Nil; got ok=%v err=%v", ok, err)
	}
}

// failingClient is a minimal RedisClient that returns the same error
// from every command. Used to assert error-path translation.
type failingClient struct{ err error }

func (f *failingClient) Get(_ context.Context, _ string) *redis.StringCmd {
	c := redis.NewStringCmd(context.Background())
	c.SetErr(f.err)
	return c
}

func (f *failingClient) Set(_ context.Context, _ string, _ any, _ time.Duration) *redis.StatusCmd {
	c := redis.NewStatusCmd(context.Background())
	c.SetErr(f.err)
	return c
}

func (f *failingClient) Del(_ context.Context, _ ...string) *redis.IntCmd {
	c := redis.NewIntCmd(context.Background())
	c.SetErr(f.err)
	return c
}

func (f *failingClient) Ping(_ context.Context) *redis.StatusCmd {
	c := redis.NewStatusCmd(context.Background())
	c.SetErr(f.err)
	return c
}
