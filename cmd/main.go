package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/observability"
	"github.com/sksmith/go-micro-example/core/secrets"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/db/usrrepo"
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

	ir := invrepo.NewPostgresRepo(dbPool)

	invService := inventory.NewService(ir, iq)

	ur := usrrepo.NewPostgresRepo(dbPool)

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
	r := api.ConfigureRouter(cfg, invService, invService, userService, signer, readinessDeps)

	_ = queue.NewProductQueue(ctx, cfg, invService)

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
