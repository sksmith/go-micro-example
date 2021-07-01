package main

import (
	"context"
	"fmt"
	queue2 "github.com/sksmith/go-micro-example/queue"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/docgen"
	"github.com/go-chi/render"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/bunnyq"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/db/usrrepo"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	ApplicationName = "go-micro-example"
	Revision        = "2"
)

var (
	AppVersion  string
	Sha1Version string
	BuildTime   string

	configUrl    = os.Getenv("CONFIG_SERVER_URL")
	configBranch = os.Getenv("CONFIG_SERVER_BRANCH")
	configUser   = os.Getenv("CONFIG_SERVER_USER")
	configPass   = os.Getenv("CONFIG_SERVER_PASS")
	profile      = os.Getenv("PROFILE")
	printConfigs = getPrintConfigs()
)

func getPrintConfigs() bool {
	v, err := strconv.ParseBool(os.Getenv("PRINT_CONFIGS"))
	if err != nil {
		return false
	}
	return v
}

func main() {
	ctx := context.Background()

	config := loadConfigs()

	configLogging(config)
	printLogHeader(config)
	dbPool := configDatabase(ctx, config)
	queue := configQueue(config)

	log.Info().Msg("creating inventory service...")
	ir := invrepo.NewPostgresRepo(dbPool)
	inventoryService := inventory.NewService(ir, queue, config.QInventoryExchange, config.QReservationExchange)

	log.Info().Msg("creating user service...")
	ur := usrrepo.NewPostgresRepo(dbPool)
	userService := user.NewService(ur)

	log.Info().Msg("configuring metrics...")
	api.ConfigureMetrics()

	log.Info().Msg("configuring router...")
	r := configureRouter(inventoryService, userService)

	if config.GenerateRoutes {
		log.Info().Msg("generating routes...")
		createRouteDocs(r)
	}

	log.Info().Str("port", config.Port).Msg("listening")
	log.Fatal().Err(http.ListenAndServe(":"+config.Port, r))
}

func configQueue(config *Config) (queue inventory.Queue) {
	if config.QMock {
		log.Info().Msg("creating mock queue...")
		queue = queue2.NewMockQueue()
	} else {
		log.Info().Msg("connecting to rabbitmq...")
		queue = rabbit(config)
	}

	return queue
}

func loadConfigs() (config *Config) {
	var err error

	if profile == "local" || profile == "" {
		log.Info().Msg("loading local configurations...")
		config, err = LoadLocalConfigs()
	} else {
		config, err = LoadRemoteConfigs(configUrl, configBranch, configUser, configPass, profile)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	config.Revision = Revision

	return config
}

func rabbit(config *Config) inventory.Queue {
	osChannel := make(chan os.Signal, 1)
	signal.Notify(osChannel, syscall.SIGTERM)
	var bq *bunnyq.BunnyQ

	for {
		bq = bunnyq.New(context.Background(),
			bunnyq.Address{
				User: config.QUser,
				Pass: config.QPass,
				Host: config.QHost,
				Port: config.QPort,
			},
			osChannel,
			bunnyq.LogHandler(logger{}),
		)

		break
	}

	return queue2.New(bq, config.QInventoryExchange, config.QReservationExchange)
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

func printLogHeader(c *Config) {
	if c.LogText {
		log.Info().Msg("=============================================")
		log.Info().Msg(fmt.Sprintf("    Application: %s", ApplicationName))
		log.Info().Msg(fmt.Sprintf("       Revision: %s", c.Revision))
		log.Info().Msg(fmt.Sprintf("        Profile: %s", profile))
		log.Info().Msg(fmt.Sprintf("  Config Server: %s - %s", configUrl, configBranch))
		log.Info().Msg(fmt.Sprintf("    Tag Version: %s", AppVersion))
		log.Info().Msg(fmt.Sprintf("   Sha1 Version: %s", Sha1Version))
		log.Info().Msg(fmt.Sprintf("     Build Time: %s", BuildTime))
		log.Info().Msg("=============================================")
	} else {
		log.Info().Str("application", ApplicationName).
			Str("revision", c.Revision).
			Str("version", AppVersion).
			Str("sha1ver", Sha1Version).
			Str("build-time", BuildTime).
			Str("profile", profile).
			Str("config-url", configUrl).
			Str("config-branch", configBranch).
			Send()
	}
}

func configDatabase(ctx context.Context, config *Config) (dbPool *pgxpool.Pool) {
	if !config.InMemoryDb {
		log.Info().Str("host", config.DbHost).Str("name", config.DbName).Msg("connecting to the database...")
		var err error

		if config.DbMigrate {
			log.Info().Msg("executing migrations")

			if err = db.RunMigrations(
				config.DbHost,
				config.DbName,
				config.DbPort,
				config.DbUser,
				config.DbPass,
				config.DbClean); err != nil {
				log.Warn().Err(err).Msg("error executing migrations")
			}
		}

		connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s",
			config.DbHost, config.DbPort, config.DbUser, config.DbPass, config.DbName)

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

func configureRouter(service inventory.Service, userService user.Service) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(api.MetricsMiddleware)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(api.LoggingMiddleware)

	r.Handle("/metrics", promhttp.Handler())
	r.With(api.Authenticate(userService)).Route("/inventory", inventoryApi(service))
	r.With(api.Authenticate(userService)).Route("/user", userApi(userService))

	return r
}

func userApi(s user.Service) func(r chi.Router) {
	userApi := api.NewUserApi(s)
	return userApi.ConfigureRouter
}

func inventoryApi(s inventory.Service) func(r chi.Router) {
	invApi := api.NewUserApi(s)
	return invApi.ConfigureRouter
}

func createRouteDocs(r chi.Router) {
	fmt.Println(docgen.MarkdownRoutesDoc(r, docgen.MarkdownOpts{
		ProjectPath: "github.com/sksmith/" + ApplicationName,
		Intro:       "The generated API documentation for " + ApplicationName,
	}))
}

func configLogging(config *Config) {
	log.Info().Msg("configuring logging...")

	if config.LogText {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	level, err := zerolog.ParseLevel(config.LogLevel)
	if err != nil {
		log.Warn().Str("loglevel", config.LogLevel).Err(err).Msg("defaulting to info")
		level = zerolog.InfoLevel
	}
	log.Info().Str("loglevel", level.String()).Msg("setting log level")
	zerolog.SetGlobalLevel(level)
}
