package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	inventoryService := inventory.NewService(ir, q, cfg.RabbitMQ.Inventory.Exchange, cfg.RabbitMQ.Reservation.Exchange)

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

	log.Info().Str("port", cfg.Port).Msg("listening")
	log.Fatal().Err(http.ListenAndServe(":"+cfg.Port, r))
}

func configInventoryQueue(bq *bunnyq.BunnyQ, cfg *config.Config) (q inventory.Queue) {
	if cfg.RabbitMQ.Mock {
		log.Info().Msg("creating mock queue...")
		return queue.NewMockQueue()
	} else {
		log.Info().Msg("connecting to rabbitmq...")
		return queue.New(bq, cfg.RabbitMQ.Inventory.Exchange, cfg.RabbitMQ.Reservation.Exchange)
	}
}

func configProductQueue(bq *bunnyq.BunnyQ, cfg *config.Config) (q *queue.ProductQueue) {
	return queue.NewProductQueue(bq, cfg.RabbitMQ.Product.Queue, cfg.RabbitMQ.Product.Dlt.Exchange)
}

func rabbit(cfg *config.Config) *bunnyq.BunnyQ {
	osChannel := make(chan os.Signal, 1)
	signal.Notify(osChannel, syscall.SIGTERM)
	var bq *bunnyq.BunnyQ

	for {
		bq = bunnyq.New(context.Background(),
			bunnyq.Address{
				User: cfg.RabbitMQ.User,
				Pass: cfg.RabbitMQ.Pass,
				Host: cfg.RabbitMQ.Host,
				Port: cfg.RabbitMQ.Port,
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
	if cfg.Log.Structured {
		log.Info().Str("application", cfg.AppName).
			Str("revision", cfg.Revision).
			Str("version", cfg.AppVersion).
			Str("sha1ver", cfg.Sha1Version).
			Str("build-time", cfg.BuildTime).
			Str("profile", cfg.Profile).
			Str("config-source", cfg.Config.Source).
			Str("config-branch", cfg.Config.Spring.Branch).
			Send()
	} else {
		f := figure.NewFigure(cfg.AppName, "", true)
		f.Print()

		log.Info().Msg("=============================================")
		log.Info().Msg(fmt.Sprintf("       Revision: %s", cfg.Revision))
		log.Info().Msg(fmt.Sprintf("        Profile: %s", cfg.Profile))
		log.Info().Msg(fmt.Sprintf("  Config Server: %s - %s", cfg.Config.Source, cfg.Config.Spring.Branch))
		log.Info().Msg(fmt.Sprintf("    Tag Version: %s", cfg.AppVersion))
		log.Info().Msg(fmt.Sprintf("   Sha1 Version: %s", cfg.Sha1Version))
		log.Info().Msg(fmt.Sprintf("     Build Time: %s", cfg.BuildTime))
		log.Info().Msg("=============================================")
	}
}

func configDatabase(ctx context.Context, cfg *config.Config) (dbPool *pgxpool.Pool) {
	if !cfg.Db.InMemory {
		log.Info().Str("host", cfg.Db.Host).Str("name", cfg.Db.Name).Msg("connecting to the database...")
		var err error

		if cfg.Db.Migrate {
			log.Info().Msg("executing migrations")

			if err = db.RunMigrations(
				cfg.Db.Host,
				cfg.Db.Name,
				cfg.Db.Port,
				cfg.Db.User,
				cfg.Db.Pass,
				cfg.Db.Clean); err != nil {
				log.Warn().Err(err).Msg("error executing migrations")
			}
		}

		connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s",
			cfg.Db.Host, cfg.Db.Port, cfg.Db.User, cfg.Db.Pass, cfg.Db.Name)

		for {
			dbPool, err = db.ConnectDb(ctx, connStr, db.MinPoolConns(10), db.MaxPoolConns(50))
			if err != nil {
				log.Error().Err(err).Msg("failed to create connection pool... retrying")
				time.Sleep(1 * time.Second)
				continue
			}
			break
		}
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

	if !cfg.Log.Structured {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	level, err := zerolog.ParseLevel(cfg.Log.Level)
	if err != nil {
		log.Warn().Str("loglevel", cfg.Log.Level).Err(err).Msg("defaulting to info")
		level = zerolog.InfoLevel
	}
	log.Info().Str("loglevel", level.String()).Msg("setting log level")
	zerolog.SetGlobalLevel(level)
}
