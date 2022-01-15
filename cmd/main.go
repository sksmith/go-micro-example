package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/render"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/bunnyq"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/db/usrrepo"
	"github.com/sksmith/go-micro-example/queue"

	"github.com/common-nighthawk/go-figure"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	ctx := context.Background()

	cfg := config.Load()

	configLogging(cfg)
	printLogHeader(cfg)
	cfg.Print()

	dbPool := configDatabase(ctx, cfg)
	bq := rabbit(cfg)
	q := configInventoryQueue(bq, cfg)

	log.Info().Msg("creating inventory service...")
	ir := invrepo.NewPostgresRepo(dbPool)
	inventoryService := inventory.NewService(ir, q, cfg.RabbitMQ.Inventory.Exchange.Value, cfg.RabbitMQ.Reservation.Exchange.Value)

	log.Info().Msg("creating user service...")
	ur := usrrepo.NewPostgresRepo(dbPool)
	userService := user.NewService(ur)

	log.Info().Msg("configuring metrics...")
	api.ConfigureMetrics()

	log.Info().Msg("configuring router...")
	r := configureRouter(cfg, inventoryService, userService)

	log.Info().Msg("consuming products...")
	prodQueue := configProductQueue(bq, cfg)
	go prodQueue.ConsumeProducts(context.Background(), inventoryService)

	log.Info().Str("port", cfg.Port.Value).Msg("listening")
	log.Fatal().Err(http.ListenAndServe(":"+cfg.Port.Value, r))
}

func configInventoryQueue(bq *bunnyq.BunnyQ, cfg *config.Config) (q inventory.Queue) {
	if cfg.RabbitMQ.Mock.Value {
		log.Info().Msg("creating mock queue...")
		return queue.NewMockQueue()
	} else {
		log.Info().Msg("connecting to rabbitmq...")
		return queue.New(bq, cfg.RabbitMQ.Inventory.Exchange.Value, cfg.RabbitMQ.Reservation.Exchange.Value)
	}
}

func configProductQueue(bq *bunnyq.BunnyQ, cfg *config.Config) (q *queue.ProductQueue) {
	return queue.NewProductQueue(bq, cfg.RabbitMQ.Product.Queue.Value, cfg.RabbitMQ.Product.Dlt.Exchange.Value)
}

func rabbit(cfg *config.Config) *bunnyq.BunnyQ {
	osChannel := make(chan os.Signal, 1)
	signal.Notify(osChannel, syscall.SIGTERM)
	var bq *bunnyq.BunnyQ

	for {
		bq = bunnyq.New(context.Background(),
			bunnyq.Address{
				User: cfg.RabbitMQ.User.Value,
				Pass: cfg.RabbitMQ.Pass.Value,
				Host: cfg.RabbitMQ.Host.Value,
				Port: cfg.RabbitMQ.Port.Value,
			},
			osChannel,
			bunnyq.LogHandler(logger{}),
		)

		break
	}

	return bq
}

type logger struct {
}

func (l logger) Log(_ context.Context, level bunnyq.LogLevel, msg string, data map[string]interface{}) {
	var evt *zerolog.Event
	switch level {
	case bunnyq.LogLevelTrace:
		evt = log.Trace()
	case bunnyq.LogLevelDebug:
		evt = log.Debug()
	case bunnyq.LogLevelInfo:
		evt = log.Info()
	case bunnyq.LogLevelWarn:
		evt = log.Warn()
	case bunnyq.LogLevelError:
		evt = log.Error()
	case bunnyq.LogLevelNone:
		evt = log.Info()
	default:
		evt = log.Info()
	}

	for k, v := range data {
		evt.Interface(k, v)
	}

	evt.Msg(msg)
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
		log.Info().Msg(fmt.Sprintf("       Revision: %s", cfg.Revision.Value))
		log.Info().Msg(fmt.Sprintf("        Profile: %s", cfg.Profile.Value))
		log.Info().Msg(fmt.Sprintf("  Config Server: %s - %s", cfg.Config.Source.Value, cfg.Config.Spring.Branch.Value))
		log.Info().Msg(fmt.Sprintf("    Tag Version: %s", cfg.AppVersion.Value))
		log.Info().Msg(fmt.Sprintf("   Sha1 Version: %s", cfg.Sha1Version.Value))
		log.Info().Msg(fmt.Sprintf("     Build Time: %s", cfg.BuildTime.Value))
		log.Info().Msg("=============================================")
	}
}

func configDatabase(ctx context.Context, cfg *config.Config) (dbPool *pgxpool.Pool) {
	if !cfg.Db.InMemory.Value {
		db.ConnectDb(ctx, cfg)
	}

	return dbPool
}

func configureRouter(cfg *config.Config, service inventory.Service, userService user.Service) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(api.Metrics)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(api.Logging)

	r.Handle("/metrics", promhttp.Handler())
	r.Route("/env", envApi(cfg))
	r.With(api.Authenticate(userService)).Route("/api", func(r chi.Router) {
		r.Route("/inventory", inventoryApi(service))
		r.Route("/user", userApi(userService))
	})

	return r
}

func userApi(s user.Service) func(r chi.Router) {
	userApi := api.NewUserApi(s)
	return userApi.ConfigureRouter
}

func inventoryApi(s inventory.Service) func(r chi.Router) {
	invApi := api.NewInventoryApi(s)
	return invApi.ConfigureRouter
}

func envApi(cfg *config.Config) func(r chi.Router) {
	envApi := api.NewEnvApi(cfg)
	return envApi.ConfigureRouter
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
