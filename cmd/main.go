package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/auth"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/secrets"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/db/usrrepo"
	"github.com/sksmith/go-micro-example/queue"

	"github.com/common-nighthawk/go-figure"
)

func main() {
	start := time.Now()
	ctx := context.Background()

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

	r := api.ConfigureRouter(cfg, invService, invService, userService, signer)

	_ = queue.NewProductQueue(ctx, cfg, invService)

	log.Info().Str("port", cfg.Port.Value).Int64("startTimeMs", time.Since(start).Milliseconds()).Msg("listening")
	srv := &http.Server{
		Addr:              ":" + cfg.Port.Value,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal().Err(srv.ListenAndServe())
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
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
}
