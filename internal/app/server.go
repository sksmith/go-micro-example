package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/common-nighthawk/go-figure"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/auth"
	"github.com/sksmith/go-micro-example/internal/catalog"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/cache"
	"github.com/sksmith/go-micro-example/internal/platform/events"
	"github.com/sksmith/go-micro-example/internal/platform/httpx"
	"github.com/sksmith/go-micro-example/internal/platform/idempotency"
	restidempotency "github.com/sksmith/go-micro-example/internal/platform/idempotency/rest"
	gmekafka "github.com/sksmith/go-micro-example/internal/platform/messaging/kafka"
	"github.com/sksmith/go-micro-example/internal/platform/observability"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/platform/ratelimit"
	"github.com/sksmith/go-micro-example/internal/user"
)

// defaultShutdownTimeout caps how long Run will wait for in-flight
// HTTP requests to drain after a SIGTERM/SIGINT. The orchestrator
// (Kubernetes by default) sends SIGKILL after terminationGracePeriodSeconds
// — make sure this stays below that. Override with
// GME_SHUTDOWN_TIMEOUT_SECONDS.
const defaultShutdownTimeout = 30 * time.Second

// defaultSamplingRatio is the root-span sample rate used when
// OTEL_TRACES_SAMPLER_ARG is unset. 10% is the value DSN-004
// recommends as a starting point — high enough to spot drift in
// busy paths, low enough not to flood the collector. Child spans
// inherit their parent's sampling decision (parent-based).
const defaultSamplingRatio = 0.10

// Deps holds the wired dependencies the Server needs to serve HTTP.
// Production wiring builds it via buildDeps from a *config.Config;
// tests construct it directly with stubs/in-memory adapters and
// hand it to NewWithDeps so the composition root stays exercisable
// without external resources.
type Deps struct {
	DB                *pgxpool.Pool
	Redis             *redis.Client
	InventorySvc      InventoryServices // satisfies inventory.InventoryService + ReservationService
	UserService       user.UserService
	Signer            *auth.Signer
	CatalogClient     catalog.Client
	ReadinessDeps     map[string]Pinger
	IdempotencyMw     func(http.Handler) http.Handler
	AuthRateLimitMw   func(http.Handler) http.Handler
	GlobalRateLimitMw func(http.Handler) http.Handler
	BodyLimitMw       func(http.Handler) http.Handler
	TracingShutdown   observability.ShutdownFunc
	KafkaCleanup      func()
}

// InventoryServices captures the slice of inventory service surface
// the router needs. The concrete *inventory.Service satisfies it (it
// already implements both InventoryService and ReservationService).
type InventoryServices interface {
	inventory.InventoryService
	inventory.ReservationService
}

// Server is the composition root. New wires production dependencies;
// NewWithDeps lets tests supply fakes. Run blocks until ctx is
// cancelled, then performs graceful shutdown.
type Server struct {
	cfg  *config.Config
	deps Deps
	http *http.Server
}

// New constructs a fully-wired Server: opens the Postgres pool,
// builds the Redis client (optional), starts AMQP publishers/
// consumers, wires services, and constructs the HTTP listener.
// Returns an error if any non-optional dependency fails to start.
func New(ctx context.Context, cfg *config.Config) (*Server, error) {
	deps, err := buildDeps(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return newWith(cfg, deps), nil
}

// NewWithDeps constructs a Server from caller-supplied Deps. Used by
// tests to inject in-memory or stub adapters. Callers must populate
// the fields the test path exercises; the constructor does no
// external-resource setup.
func NewWithDeps(cfg *config.Config, deps Deps) *Server {
	return newWith(cfg, deps)
}

func newWith(cfg *config.Config, deps Deps) *Server {
	r := ConfigureRouter(
		cfg,
		deps.InventorySvc, deps.InventorySvc,
		deps.UserService,
		deps.Signer,
		deps.ReadinessDeps,
		deps.CatalogClient,
		deps.IdempotencyMw,
		deps.AuthRateLimitMw,
		deps.GlobalRateLimitMw,
		deps.BodyLimitMw,
	)
	srv := &http.Server{
		Addr:              ":" + cfg.Port.Value,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if cfg.TLS.Enabled.Value {
		srv.TLSConfig = modernTLSConfig()
	}
	return &Server{cfg: cfg, deps: deps, http: srv}
}

// modernTLSConfig returns the TLS settings the service uses when it
// terminates HTTPS itself. The profile matches Mozilla's
// "intermediate" recommendation: TLS 1.2+ only, AEAD cipher suites,
// forward-secret key exchange. TLS 1.3 cipher suites and curves are
// fixed by Go and need no explicit listing.
func modernTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
		},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	}
}

// Handler exposes the chi router so tests can hit it via httptest
// without binding a real socket.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Run starts the HTTP listener and blocks until ctx is cancelled or
// the listener fails. On shutdown it runs Cleanup in the same order
// production used to: HTTP drain → DB close → tracing flush.
func (s *Server) Run(ctx context.Context) error {
	start := time.Now()

	if s.cfg.TLS.Enabled.Value {
		if s.cfg.TLS.CertFile.Value == "" || s.cfg.TLS.KeyFile.Value == "" {
			return fmt.Errorf("tls.enabled=true requires tls.certFile and tls.keyFile")
		}
	}

	serveErr := make(chan error, 1)
	go func() {
		var err error
		if s.cfg.TLS.Enabled.Value {
			log.Info().
				Str("port", s.cfg.Port.Value).
				Str("certFile", s.cfg.TLS.CertFile.Value).
				Int64("startTimeMs", time.Since(start).Milliseconds()).
				Msg("listening (TLS)")
			err = s.http.ListenAndServeTLS(s.cfg.TLS.CertFile.Value, s.cfg.TLS.KeyFile.Value)
		} else {
			log.Info().Str("port", s.cfg.Port.Value).Int64("startTimeMs", time.Since(start).Milliseconds()).Msg("listening")
			err = s.http.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	var listenErr error
	select {
	case listenErr = <-serveErr:
		if listenErr != nil {
			log.Error().Err(listenErr).Msg("HTTP server failed")
		}
	case <-ctx.Done():
		log.Info().Str("signal", "received").Msg("graceful shutdown beginning")
	}

	s.Cleanup()
	return listenErr
}

// Cleanup runs the ordered teardown after Run returns. Order:
//
//  1. Stop accepting new HTTP requests; let in-flight requests
//     drain up to the configured timeout.
//  2. Stop the Kafka consumer (if running).
//  3. Close the pgx pool.
//  4. Close the Redis client (if wired).
//  5. Flush the OTel tracer provider.
//
// AMQP consumer drain (the queue subsystem) is deferred to TST-003.
func (s *Server) Cleanup() {
	shutdownHTTP(s.http, resolveShutdownTimeout())

	if s.deps.KafkaCleanup != nil {
		s.deps.KafkaCleanup()
	}

	if s.deps.DB != nil {
		log.Info().Msg("closing database pool")
		s.deps.DB.Close()
	}

	if s.deps.Redis != nil {
		if err := s.deps.Redis.Close(); err != nil {
			log.Warn().Err(err).Msg("redis close returned an error")
		}
	}

	if s.deps.TracingShutdown != nil {
		log.Info().Msg("flushing tracer")
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.deps.TracingShutdown(flushCtx); err != nil {
			log.Error().Err(err).Msg("tracer shutdown returned an error")
		}
	}

	log.Info().Msg("shutdown complete")
}

// shutdownHTTP stops the server's listeners and waits for in-flight
// requests to finish, up to the supplied timeout. On timeout (or any
// other Shutdown error) it falls back to srv.Close, which drops the
// remaining idle connections immediately.
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

// ConfigLogging applies the structured/console logger format and
// log-level from cfg. Called from cmd/server/main before anything
// else logs so the chosen format is in effect for startup.
func ConfigLogging(cfg *config.Config) {
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

// PrintLogHeader emits the startup banner (figure ASCII when
// structured logs are off, structured fields when on).
func PrintLogHeader(cfg *config.Config) {
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

// buildDeps performs the production wiring: opens Postgres, builds
// Redis (optional), starts tracing, constructs services, mounts the
// AMQP publishers, starts the product consumer and Kafka pair when
// configured. Returns a populated Deps. Non-optional failures are
// returned as errors; optional integrations (Redis, catalog client,
// Kafka) log and degrade.
func buildDeps(ctx context.Context, cfg *config.Config) (Deps, error) {
	tracingShutdown, err := observability.InitTracing(ctx, observability.TracingConfig{
		ServiceName:    cfg.AppName.Value,
		ServiceVersion: cfg.AppVersion.Value,
		SamplingRatio:  observability.ResolveSamplingRatio(defaultSamplingRatio),
	})
	if err != nil {
		return Deps{}, fmt.Errorf("tracing init: %w", err)
	}

	dbPool, err := persistence.ConnectDb(ctx, cfg)
	if err != nil {
		return Deps{}, fmt.Errorf("connect db: %w", err)
	}

	iq := inventory.NewInventoryQueue(ctx, cfg)
	ir := inventory.NewPostgresRepo(dbPool)
	invService := inventory.NewService(ir, iq)

	redisClient := buildRedisClient(cfg)
	if redisClient != nil {
		invService.SetCache(cache.NewRedisCache(redisClient), time.Duration(cfg.Redis.CacheTTLMinutes.Value)*time.Minute)
	}

	ur := user.NewPostgresRepo(dbPool)
	if redisClient != nil {
		ur.SetCache(cache.NewRedisCache(redisClient), time.Duration(cfg.Redis.UserCacheTTLSeconds.Value)*time.Second)
	}
	userService := user.NewService(ur)

	if err := user.Bootstrap(ctx, ur, cfg.Profile.Value, os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")); err != nil {
		return Deps{}, fmt.Errorf("admin bootstrap: %w", err)
	}

	signer, err := configureSigner(cfg.Profile.Value)
	if err != nil {
		return Deps{}, err
	}

	readinessDeps := map[string]Pinger{"db": dbPool}
	if redisClient != nil {
		readinessDeps["redis"] = redisPinger{client: redisClient}
	}

	catalogClient := buildCatalogClient(cfg)
	idempotencyMw := buildIdempotencyMiddleware(cfg, redisClient)
	authRateLimitMw := buildAuthRateLimitMiddleware(cfg, redisClient)
	globalRateLimitMw := buildGlobalRateLimitMiddleware(cfg, redisClient)
	bodyLimitMw := buildBodyLimitMiddleware(cfg)

	_ = inventory.NewProductQueue(ctx, cfg, invService)

	kafkaCleanup := func() {}
	if cfg.Kafka.Brokers.Value != "" {
		kafkaCleanup = startKafka(ctx, cfg, invService, dbPool)
	}

	return Deps{
		DB:                dbPool,
		Redis:             redisClient,
		InventorySvc:      invService,
		UserService:       userService,
		Signer:            signer,
		CatalogClient:     catalogClient,
		ReadinessDeps:     readinessDeps,
		IdempotencyMw:     idempotencyMw,
		AuthRateLimitMw:   authRateLimitMw,
		GlobalRateLimitMw: globalRateLimitMw,
		BodyLimitMw:       bodyLimitMw,
		TracingShutdown:   tracingShutdown,
		KafkaCleanup:      kafkaCleanup,
	}, nil
}

// redisPinger adapts a *redis.Client to Pinger so /ready can verify
// Redis connectivity. The native Redis Ping returns a *StatusCmd
// instead of an error directly; wrap to match the interface.
type redisPinger struct{ client *redis.Client }

func (r redisPinger) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

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
	return httpx.Middleware(limiter, httpx.IPKeyScoped("rl:auth:", "auth"))
}

// buildGlobalRateLimitMiddleware constructs the SEC-007 per-IP global
// throttle. It uses its own Redis-key prefix ("rl:global:") and scope
// label ("global") so the bucket is independent of the stricter
// /auth/token bucket and the two show up as separate series on the
// ratelimit_* metrics. Disabled when redis.url is empty — the limiter
// needs Redis to coordinate across replicas.
func buildGlobalRateLimitMiddleware(cfg *config.Config, redisClient *redis.Client) func(http.Handler) http.Handler {
	if redisClient == nil {
		log.Info().Msg("global rate limiter disabled (no redis client)")
		return func(next http.Handler) http.Handler { return next }
	}
	limiter := ratelimit.New(redisClient, ratelimit.Config{
		Rate:  cfg.RateLimit.GlobalRatePerSecond.Value,
		Burst: int(cfg.RateLimit.GlobalBurst.Value),
	})
	log.Info().
		Float64("rate", cfg.RateLimit.GlobalRatePerSecond.Value).
		Int64("burst", cfg.RateLimit.GlobalBurst.Value).
		Msg("global rate limiter ready")
	return httpx.Middleware(limiter, httpx.IPKeyScoped("rl:global:", "global"))
}

// buildBodyLimitMiddleware constructs the SEC-007 request-body cap. A
// zero or negative configured value falls back to httpx's 1-MiB
// default; an explicit positive value overrides it.
func buildBodyLimitMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	limit := cfg.RateLimit.MaxRequestBodyBytes.Value
	if limit <= 0 {
		limit = httpx.DefaultMaxRequestBodyBytes
	}
	log.Info().Int64("limit_bytes", limit).Msg("request body size limit ready")
	return httpx.MaxBytes(limit)
}

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

func configureSigner(profile string) (*auth.Signer, error) {
	key := []byte(os.Getenv("GME_JWT_SIGNING_KEY"))
	ttl := time.Duration(0)
	if raw := os.Getenv("GME_JWT_TTL_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return nil, fmt.Errorf("invalid GME_JWT_TTL_SECONDS=%q; expected positive integer", raw)
		}
		ttl = time.Duration(seconds) * time.Second
	}

	strict := profile == "prod"
	signer, err := auth.NewSigner(key, ttl, strict)
	if err != nil {
		return nil, fmt.Errorf("construct jwt signer: %w", err)
	}
	if signer.Ephemeral() {
		log.Warn().
			Dur("ttl", signer.TTL()).
			Msg("GME_JWT_SIGNING_KEY missing or shorter than 32 bytes; using ephemeral key. Tokens will not survive a restart. Set the env var for stable issuance.")
	} else {
		log.Info().Dur("ttl", signer.TTL()).Msg("jwt signer ready")
	}
	return signer, nil
}

// kafkaInventoryService is the surface startKafka needs from the
// inventory service. Defining it inline keeps this package off the
// package-private *inventory.service while still requiring the
// methods the Kafka adapters call.
type kafkaInventoryService interface {
	SetEventEmitter(inventory.EventEmitter)
	GetProduct(ctx context.Context, sku string) (inventory.Product, error)
	Produce(ctx context.Context, product inventory.Product, event inventory.ProductionRequest) error
}

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

type idempotentHandler struct {
	applier *idempotency.Applier
	inner   gmekafka.Handler
}

func (h idempotentHandler) Handle(ctx context.Context, env events.Envelope) error {
	return h.applier.Apply(ctx, env.EventID, func(ctx context.Context) error {
		return h.inner.Handle(ctx, env)
	})
}
