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
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/app"
	"github.com/sksmith/go-micro-example/internal/platform/secrets"
)

func main() {
	// signal.NotifyContext cancels on SIGINT (Ctrl-C) or SIGTERM
	// (Kubernetes pod eviction, systemd stop). Long-lived subsystems
	// (queue redial loops) thread this ctx so they unwind together.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// Resolve secrets from the configured provider before viper reads
	// the env (DSN-006). The provider populates GME_* env vars so the
	// existing viper.BindEnv plumbing keeps working unchanged.
	if err := secrets.LoadFromEnv(); err != nil {
		log.Fatal().Err(err).Msg("secrets provider failed; refusing to start")
	}

	cfg := config.Load("config")

	app.ConfigLogging(cfg)
	app.PrintLogHeader(cfg)
	cfg.Print()

	srv, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("server init failed")
	}

	if err := srv.Run(ctx); err != nil {
		os.Exit(1)
	}
}
