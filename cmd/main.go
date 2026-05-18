// Package main starts the go-micro-example HTTP server.
//
//	@title			go-micro-example API
//	@version		1.0
//	@description	Reference API for the go-micro-example service: inventory, reservations, users, and an admin/env probe.
//	@description	All errors use RFC 7807 (application/problem+json).
//	@BasePath		/
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Bearer JWT issued by POST /auth/token. To obtain one, send HTTP Basic credentials to /auth/token.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/cache"
	"github.com/sksmith/go-micro-example/core/catalog"
	"github.com/sksmith/go-micro-example/core/observability"
	"github.com/sksmith/go-micro-example/core/ratelimit"
	"github.com/sksmith/go-micro-example/core/secrets"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/events"
	"github.com/sksmith/go-micro-example/idempotency"
	restidempotency "github.com/sksmith/go-micro-example/idempotency/rest"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/user"
	gmekafka "github.com/sksmith/go-micro-example/kafka"
	"github.com/sksmith/go-micro-example/queue"

	"github.com/common-nighthawk/go-figure"
)

// defaultShutdownTimeout caps how long we'll wait for in-flight
// HTTP requests to drain after a SIGTERM/SIGINT. The orchestrator
// (Kubernetes by default) sends SIGKILL after terminationGracePeriodSeconds
// — make sure this stays below that. Override with
// GME_SHUTDOWN_TIMEOUT_SECONDS, matching the same env-var pattern
// used by GME_JWT_TTL_SECONDS.
const defaultShutdownTimeout = 30 * time.Second

// defaultSamplingRatio is the root-span sample rate used when
// OTEL_TRACES_SAMPLER_ARG is unset. 10% is the value DSN-004
// recommends as a starting point — high enough to spot drift in
// busy paths, low enough not to flood the collector. Child spans
// inherit their parent's sampling decision (parent-based).
const defaultSamplingRatio = 0.10

func main() {
	start := time.Now()

	// signal.NotifyContext gives us a context that cancels on
	// SIGINT (Ctrl-C) or SIGTERM (Kubernetes pod eviction,
	// systemd stop). Long-lived subsystems (queue redial loops)
	// thread this ctx so they unwind together.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// Resolve secrets from the configured provider before viper reads
	// the env (DSN-006). The provider populates GME_* env vars so the
	// existing viper.BindEnv plumbing keeps working unchanged.
	if err := secrets.LoadFromEnv(); err != nil {
		log.Fatal().Err(err).Msg("secrets provider failed; refusing to start")
	}

	cfg := config.Load("config")

	configLogging(cfg)
	printLogHeader(cfg)
	cfg.Print()

	// Tracing initialises before anything that emits spans. When
	// OTEL_EXPORTER_OTLP_ENDPOINT is unset (local dev / tests)
	// this installs a no-op provider — call sites still tracer.Start
	// freely, the spans just go nowhere.
	tracingShutdown, err := observability.InitTracing(ctx, observability.TracingConfig{
		ServiceName:    cfg.AppName.Value,
		ServiceVersion: cfg.AppVersion.Value,
		SamplingRatio:  observability.ResolveSamplingRatio(defaultSamplingRatio),
	})
	if err != nil {
		log.Fatal().Err(err).Msg("tracing init failed")
	}

	dbPool := configDatabase(ctx, cfg)

	iq := queue.NewInventoryQueue(ctx, cfg)

	ir := inventory.NewPostgresRepo(dbPool)

	invService := inventory.NewService(ir, iq)

	// DSN-020: Redis cache. An empty URL leaves invService.cache nil
	// and the inventory read path falls through to the DB unchanged.
	redisClient := buildRedisClient(cfg)
	if redisClient != nil {
		invService.SetCache(cache.NewRedisCache(redisClient), time.Duration(cfg.Redis.CacheTTLMinutes.Value)*time.Minute)
	}

	ur := user.NewPostgresRepo(dbPool)
	if redisClient != nil {
		// DSN-021c: Redis-backed user cache replaces the in-process
		// LRU. Short TTL so revocations propagate without explicit
		// cache-bust on the user-management endpoints.
		ur.SetCache(cache.NewRedisCache(redisClient), time.Duration(cfg.Redis.UserCacheTTLSeconds.Value)*time.Second)
	}

	userService := user.NewService(ur)

	if err := user.Bootstrap(ctx, ur, cfg.Profile.Value, os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")); err != nil {
		log.Fatal().Err(err).Msg("admin bootstrap failed")
	}

	signer := configureSigner(cfg.Profile.Value)

	// /ready pings every dep in this map on every probe.
	// Pass at minimum the pgx pool. AMQP readiness is deliberately
	// absent — the queue subsystem doesn't yet expose a non-blocking
	// connectivity check; tracked alongside TST-003.
	readinessDeps := map[string]api.Pinger{"db": dbPool}
	if redisClient != nil {
		readinessDeps["redis"] = redisPinger{client: redisClient}
	}
	catalogClient := buildCatalogClient(cfg)
	idempotencyMw := buildIdempotencyMiddleware(cfg, redisClient)
	authRateLimitMw := buildAuthRateLimitMiddleware(cfg, redisClient)
	r := api.ConfigureRouter(cfg, invService, invService, userService, signer, readinessDeps, catalogClient, idempotencyMw, authRateLimitMw)

	_ = queue.NewProductQueue(ctx, cfg, invService)

	// DSN-016: Kafka producer + consumer. Skipped entirely when
	// kafka.brokers is empty so the AMQP-only template path still
	// runs cleanly without a Kafka broker.
	kafkaCleanup := func() {}
	if cfg.Kafka.Brokers.Value != "" {
		kafkaCleanup = startKafka(ctx, cfg, invService, dbPool)
	}
	defer kafkaCleanup()

	srv := &http.Server{
		Addr:              ":" + cfg.Port.Value,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Run ListenAndServe in a goroutine so main can wait on
	// the signal context without blocking the listener.
	serveErr := make(chan error, 1)
	go func() {
		log.Info().Str("port", cfg.Port.Value).Int64("startTimeMs", time.Since(start).Milliseconds()).Msg("listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		// ListenAndServe returned a non-graceful error before any
		// signal arrived (e.g. port in use). Log and exit.
		log.Fatal().Err(err).Msg("HTTP server failed")
	case <-ctx.Done():
		log.Info().Str("signal", "received").Msg("graceful shutdown beginning")
	}

	shutdown(srv, dbPool, tracingShutdown)
	if redisClient != nil {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("redis close returned an error")
		}
	}
}

// redisPinger adapts a *redis.Client to api.Pinger so /ready can
// verify Redis connectivity. The native Redis Ping returns a
// (*StatusCmd) instead of an error directly; wrap to match the
// interface.
type redisPinger struct{ client *redis.Client }

func (r redisPinger) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// buildRedisClient constructs the shared Redis client for DSN-020.
// An empty redis.url disables the client; returning nil tells the
// rest of the wiring to skip cache plumbing. Construction errors are
// logged and downgraded to nil: a misconfigured Redis URL shouldn't
// take the service down — the cache is opt-in optimization, not a
// hard dependency.
func buildRedisClient(cfg *config.Config) *redis.Client {
	url := cfg.Redis.URL.Value
	if url == "" {
		log.Info().Msg("redis client disabled (redis.url empty); inventory cache off")
		return nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("redis URL parse failed; continuing without cache")
		return nil
	}
	client := redis.NewClient(opts)
	log.Info().Str("addr", opts.Addr).Int("db", opts.DB).Msg("redis client ready")
	return client
}

// buildAuthRateLimitMiddleware constructs the DSN-021b rate limiter
// for /auth/token, bucketed per source IP. Returning a pass-through
// when Redis isn't wired keeps every-other-route's behaviour
// unchanged; the rate limiter needs a shared store to be useful and
// degrades transparently when one isn't there.
func buildAuthRateLimitMiddleware(cfg *config.Config, redisClient *redis.Client) func(http.Handler) http.Handler {
	if redisClient == nil {
		log.Info().Msg("auth-token rate limiter disabled (no redis client)")
		return func(next http.Handler) http.Handler { return next }
	}
	limiter := ratelimit.New(redisClient, ratelimit.Config{
		Rate:  cfg.RateLimit.AuthRatePerSecond.Value,
		Burst: int(cfg.RateLimit.AuthBurst.Value),
	})
	log.Info().
		Float64("rate", cfg.RateLimit.AuthRatePerSecond.Value).
		Int64("burst", cfg.RateLimit.AuthBurst.Value).
		Msg("auth-token rate limiter ready")
	return ratelimit.Middleware(limiter, ratelimit.IPKey)
}

// buildIdempotencyMiddleware constructs the DSN-019 Idempotency-Key
// middleware. When a Redis client is available (DSN-021a) it backs
// the store with Redis so retries that hit a different replica after
// a load balancer failover still replay the cached response;
// otherwise it falls back to the in-memory store and the cache stays
// per-process. The middleware enforces Idempotency-Key presence on
// the mutating routes it wraps either way.
func buildIdempotencyMiddleware(cfg *config.Config, redisClient *redis.Client) func(http.Handler) http.Handler {
	ttl := time.Duration(cfg.Idempotency.TTLMinutes.Value) * time.Minute
	var store restidempotency.Store
	if redisClient != nil {
		store = restidempotency.NewRedisStore(redisClient)
		log.Info().Dur("ttl", ttl).Msg("rest idempotency middleware ready (Redis store)")
	} else {
		store = restidempotency.NewMemoryStore()
		log.Info().Dur("ttl", ttl).Msg("rest idempotency middleware ready (in-memory store; cross-replica retries will miss)")
	}
	return restidempotency.Middleware(restidempotency.Config{
		Store:    store,
		TTL:      ttl,
		Required: true,
	})
}

// buildCatalogClient constructs the outbound REST client for the
// upstream catalog service (DSN-018). An empty catalog.baseUrl
// disables the client — the inventory API then serves unenriched
// responses. Construction errors are logged and the function returns
// nil rather than failing startup: enrichment is best-effort and a
// misconfigured upstream shouldn't take the whole service offline.
func buildCatalogClient(cfg *config.Config) catalog.Client {
	if cfg.Catalog.BaseURL.Value == "" {
		log.Info().Msg("catalog client disabled (catalog.baseUrl empty)")
		return nil
	}
	c, err := catalog.NewHTTPClient(catalog.Config{
		BaseURL:           cfg.Catalog.BaseURL.Value,
		Timeout:           time.Duration(cfg.Catalog.Timeout.Value) * time.Millisecond,
		PerAttemptTimeout: time.Duration(cfg.Catalog.PerAttemptTimeout.Value) * time.Millisecond,
		MaxAttempts:       int(cfg.Catalog.MaxAttempts.Value),
	})
	if err != nil {
		log.Error().Err(err).Msg("catalog client init failed; continuing without enrichment")
		return nil
	}
	log.Info().Str("baseUrl", cfg.Catalog.BaseURL.Value).Msg("catalog client ready")
	return c
}

// kafkaInventoryService is the surface DSN-016's startKafka needs
// from the inventory service. Defining it inline keeps cmd/main from
// referencing the package-private *inventory.service while still
// requiring the methods the Kafka adapters actually call.
type kafkaInventoryService interface {
	SetEventEmitter(inventory.EventEmitter)
	GetProduct(ctx context.Context, sku string) (inventory.Product, error)
	Produce(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error
}

// startKafka builds the Kafka producer + consumer pair (DSN-016) and
// returns a cleanup function the caller defers. The consumer runs in
// its own goroutine and exits when ctx is canceled; cleanup waits for
// that exit so an in-flight handler doesn't get clipped.
//
// The handler is wrapped with the DSN-017 idempotency Applier so a
// redelivered Kafka message — common after rebalances — does not
// double-apply its side effects. A background retention job prunes
// processed_events rows older than the retention window.
func startKafka(ctx context.Context, cfg *config.Config, invService kafkaInventoryService, pool *pgxpool.Pool) func() {
	brokers := strings.Split(cfg.Kafka.Brokers.Value, ",")
	prod, err := gmekafka.NewProducer(brokers, cfg.Kafka.EventsTopic.Value)
	if err != nil {
		log.Error().Err(err).Msg("kafka producer init failed; continuing without Kafka")
		return func() {}
	}
	invService.SetEventEmitter(&inventory.InventoryEmitter{Producer: prod})

	applier := idempotency.NewApplier(pool, cfg.Kafka.ConsumerGroup.Value)
	inner := &inventory.InventoryCommandHandler{Service: invService}
	handler := idempotentHandler{applier: applier, inner: inner}

	consumer, err := gmekafka.NewConsumer(gmekafka.ConsumerConfig{
		Brokers:  brokers,
		Topic:    cfg.Kafka.CommandsTopic.Value,
		DLTTopic: cfg.Kafka.DltTopic.Value,
		Group:    cfg.Kafka.ConsumerGroup.Value,
		Handler:  handler,
	})
	if err != nil {
		log.Error().Err(err).Msg("kafka consumer init failed; producer still active")
		return func() { prod.Close() }
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if runErr := consumer.Run(ctx); runErr != nil {
			log.Error().Err(runErr).Msg("kafka consumer run returned an error")
		}
	}()

	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		applier.Cleanup(ctx, time.Hour, 30*24*time.Hour)
	}()

	return func() {
		<-done
		<-cleanupDone
		consumer.Close()
		prod.Close()
	}
}

// idempotentHandler wraps a gmekafka.Handler with the idempotency
// Applier so each (event_id, consumer_group) pair runs at most once.
// Returning the inner handler's error preserves DSN-016's bounded
// retry → DLT path; the dedupe row only commits when the inner
// handler returns nil.
type idempotentHandler struct {
	applier *idempotency.Applier
	inner   gmekafka.Handler
}

func (h idempotentHandler) Handle(ctx context.Context, env events.Envelope) error {
	return h.applier.Apply(ctx, env.EventID, func(ctx context.Context) error {
		return h.inner.Handle(ctx, env)
	})
}

// shutdown runs the ordered teardown after a SIGTERM / SIGINT:
//
//  1. Stop accepting new HTTP requests; let in-flight requests
//     drain up to the configured timeout (shutdownHTTP).
//  2. Close the pgx pool, releasing connections back to Postgres.
//  3. Flush the OTel tracer provider so any batched spans reach
//     the collector before the process exits.
//
// AMQP consumer drain (the queue subsystem) is deferred to
// TST-003 — the existing redial loop in queue/queue.go cooperates
// with ctx cancellation but doesn't expose a "finish in-flight
// deliveries then exit" surface. The fix needs the queue to grow
// a Close method, which sits more naturally with the test suite
// that ticket sets up.
func shutdown(srv *http.Server, pool *pgxpool.Pool, tracingShutdown observability.ShutdownFunc) {
	shutdownHTTP(srv, resolveShutdownTimeout())
	log.Info().Msg("closing database pool")
	pool.Close()

	log.Info().Msg("flushing tracer")
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracingShutdown(flushCtx); err != nil {
		log.Error().Err(err).Msg("tracer shutdown returned an error")
	}
	log.Info().Msg("shutdown complete")
}

// shutdownHTTP stops the server's listeners and waits for in-flight
// requests to finish, up to the supplied timeout. On timeout (or
// any other Shutdown error) it falls back to srv.Close, which drops
// the remaining idle connections immediately.
func shutdownHTTP(srv *http.Server, timeout time.Duration) {
	log.Info().Dur("timeout", timeout).Msg("shutting down HTTP server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown returned an error; forcing close")
		if closeErr := srv.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			log.Error().Err(closeErr).Msg("HTTP server force close failed")
		}
		return
	}
	log.Info().Msg("HTTP server stopped cleanly")
}

func resolveShutdownTimeout() time.Duration {
	raw := os.Getenv("GME_SHUTDOWN_TIMEOUT_SECONDS")
	if raw == "" {
		return defaultShutdownTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		log.Warn().
			Str("GME_SHUTDOWN_TIMEOUT_SECONDS", raw).
			Dur("default", defaultShutdownTimeout).
			Msg("invalid shutdown timeout; using default")
		return defaultShutdownTimeout
	}
	return time.Duration(seconds) * time.Second
}

func printLogHeader(cfg *config.Config) {
	if cfg.Log.Structured.Value {
		log.Info().Str("application", cfg.AppName.Value).
			Str("revision", cfg.Revision.Value).
			Str("version", cfg.AppVersion.Value).
			Str("sha1ver", cfg.Sha1Version.Value).
			Str("build-time", cfg.BuildTime.Value).
			Str("profile", cfg.Profile.Value).
			Str("config-source", cfg.Config.Source.Value).
			Str("config-branch", cfg.Config.Spring.Branch.Value).
			Send()
	} else {
		f := figure.NewFigure(cfg.AppName.Value, "", true)
		f.Print()

		log.Info().Msg("=============================================")
		log.Info().Msg(fmt.Sprintf("      Revision: %s", cfg.Revision.Value))
		log.Info().Msg(fmt.Sprintf("       Profile: %s", cfg.Profile.Value))
		log.Info().Msg(fmt.Sprintf(" Config Server: %s - %s", cfg.Config.Source.Value, cfg.Config.Spring.Branch.Value))
		log.Info().Msg(fmt.Sprintf("   Tag Version: %s", cfg.AppVersion.Value))
		log.Info().Msg(fmt.Sprintf("  Sha1 Version: %s", cfg.Sha1Version.Value))
		log.Info().Msg(fmt.Sprintf("    Build Time: %s", cfg.BuildTime.Value))
		log.Info().Msg("=============================================")
	}
}

func configDatabase(ctx context.Context, cfg *config.Config) *pgxpool.Pool {
	dbPool, err := db.ConnectDb(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to db")
	}

	return dbPool
}

func configureSigner(profile string) *auth.Signer {
	key := []byte(os.Getenv("GME_JWT_SIGNING_KEY"))
	ttl := time.Duration(0)
	if raw := os.Getenv("GME_JWT_TTL_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			log.Fatal().Str("GME_JWT_TTL_SECONDS", raw).Msg("invalid jwt ttl; expected positive integer seconds")
		}
		ttl = time.Duration(seconds) * time.Second
	}

	strict := profile == "prod"
	signer, err := auth.NewSigner(key, ttl, strict)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to construct jwt signer")
	}
	if signer.Ephemeral() {
		log.Warn().
			Dur("ttl", signer.TTL()).
			Msg("GME_JWT_SIGNING_KEY missing or shorter than 32 bytes; using ephemeral key. Tokens will not survive a restart. Set the env var for stable issuance.")
	} else {
		log.Info().Dur("ttl", signer.TTL()).Msg("jwt signer ready")
	}
	return signer
}

func configLogging(cfg *config.Config) {
	log.Info().Msg("configuring logging...")

	if !cfg.Log.Structured.Value {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	level, err := zerolog.ParseLevel(cfg.Log.Level.Value)
	if err != nil {
		log.Warn().Str("loglevel", cfg.Log.Level.Value).Err(err).Msg("defaulting to info")
		level = zerolog.InfoLevel
	}
	log.Info().Str("loglevel", level.String()).Msg("setting log level")
	zerolog.SetGlobalLevel(level)
}
