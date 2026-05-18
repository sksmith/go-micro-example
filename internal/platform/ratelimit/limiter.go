// Package ratelimit is the distributed-token-bucket rate limiter
// introduced by DSN-021b. The bucket lives in Redis so multiple
// replicas share one budget per (limit-prefix, identity) instead of
// each replica enforcing its own ceiling.
//
// The bucket math runs inside a Lua script (script.lua) so the
// "read tokens, refill, subtract, write back" sequence is atomic
// from Redis's perspective — two simultaneous callers can't both
// observe the same token count and both subtract. Lua is the
// canonical pattern for this on Redis and avoids reaching for a
// third-party rate-limit library to wrap ~30 lines of clarity.
//
// The package separates concerns:
//
//   - Limiter is the transport-agnostic core. Allow(ctx, key, ...)
//     returns a Decision; callers (HTTP middleware, eventual gRPC
//     interceptor, queue consumer back-pressure) translate that into
//     the right response shape.
//   - Middleware (middleware.go) is the HTTP wrapper used by
//     cmd/main on the brute-force-sensitive routes.
//
// Redis outages degrade open. The alternative — denying every
// request when Redis flaps — turns a cache outage into a service
// outage, and brute-force attempts are a worse problem than a
// momentary lapse in throttling.
package ratelimit

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

//go:embed script.lua
var luaSource string

var luaScript = redis.NewScript(luaSource)

// RedisClient is the slice of *redis.Client Limiter actually needs.
// Narrowing keeps tests fakeable without reimplementing the whole
// Cmdable surface. *redis.Client and *redis.ClusterClient satisfy
// this interface directly.
type RedisClient interface {
	redis.Scripter
}

// Config is the per-Limiter knobs. Rate is tokens-per-second; Burst
// is the bucket ceiling. KeyTTL is the Redis expiry applied to
// abandoned bucket rows — comfortably longer than (Burst / Rate)
// seconds so a paused user's bucket doesn't get swept mid-window.
type Config struct {
	Rate   float64
	Burst  int
	KeyTTL time.Duration
}

// Decision is the typed result of an Allow call. Allowed reports
// whether the request gets through; Remaining is the tokens left in
// the bucket after the call (useful for X-RateLimit-Remaining
// headers); RetryAfter is the time the caller should wait before
// trying again (zero on allow, populated on deny).
type Decision struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}

// Limiter applies the token-bucket logic against Redis.
type Limiter struct {
	client RedisClient
	cfg    Config
}

func New(client RedisClient, cfg Config) *Limiter {
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	if cfg.KeyTTL <= 0 {
		// Default: ten full-bucket windows. Long enough that a
		// paused user doesn't lose their accumulated tokens on a
		// natural pause; short enough that abandoned bucket keys
		// don't accumulate indefinitely.
		cfg.KeyTTL = time.Duration(float64(cfg.Burst)/cfg.Rate*10) * time.Second
		if cfg.KeyTTL < time.Minute {
			cfg.KeyTTL = time.Minute
		}
	}
	ensureMetrics()
	return &Limiter{client: client, cfg: cfg}
}

var (
	metricsOnce    sync.Once
	allowedTotal   *prometheus.CounterVec
	deniedTotal    *prometheus.CounterVec
	errorsTotal    prometheus.Counter
	limiterLatency prometheus.Histogram
)

func ensureMetrics() {
	metricsOnce.Do(func() {
		allowedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_allowed_total",
			Help: "Rate-limit decisions that allowed the request.",
		}, []string{"scope"})
		deniedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_denied_total",
			Help: "Rate-limit decisions that denied the request (HTTP 429).",
		}, []string{"scope"})
		errorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratelimit_errors_total",
			Help: "Lua/redis errors that forced the limiter to degrade open.",
		})
		limiterLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ratelimit_eval_duration_ms",
			Help:    "Time to evaluate one rate-limit check (ms).",
			Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100},
		})
		prometheus.MustRegister(allowedTotal, deniedTotal, errorsTotal, limiterLatency)
	})
}

// Allow runs the Lua bucket against key. A non-nil error means the
// limiter couldn't reach Redis or the script returned an unexpected
// shape — the caller should treat that as "fail open" (the Middleware
// in this package does). Scope is a label for metrics ("ip" / "user").
func (l *Limiter) Allow(ctx context.Context, key, scope string) (Decision, error) {
	start := time.Now()
	defer func() { limiterLatency.Observe(float64(time.Since(start).Microseconds()) / 1000) }()

	nowMs := time.Now().UnixMilli()
	ttlSeconds := int64(l.cfg.KeyTTL.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 60
	}

	res, err := luaScript.Run(
		ctx, l.client, []string{key},
		l.cfg.Rate, l.cfg.Burst, 1, nowMs, ttlSeconds,
	).Result()
	if err != nil {
		errorsTotal.Inc()
		return Decision{Allowed: true}, fmt.Errorf("ratelimit eval: %w", err)
	}

	parts, ok := res.([]any)
	if !ok || len(parts) < 3 {
		errorsTotal.Inc()
		return Decision{Allowed: true}, errors.New("ratelimit: unexpected script return shape")
	}

	allowed := toInt64(parts[0]) == 1
	remaining := toInt64(parts[1])
	retryAfterMs := toInt64(parts[2])

	if allowed {
		allowedTotal.WithLabelValues(scope).Inc()
	} else {
		deniedTotal.WithLabelValues(scope).Inc()
	}
	return Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryAfterMs) * time.Millisecond,
	}, nil
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	log.Warn().Interface("v", v).Msg("ratelimit: unexpected Lua return type; treating as 0")
	return 0
}
