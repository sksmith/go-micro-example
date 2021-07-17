package db

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type config struct {
	timeZone              string
	sslMode               string
	poolMaxConns          int32
	poolMinConns          int32
	poolMaxConnLifetime   time.Duration
	poolMaxConnIdleTime   time.Duration
	poolHealthCheckPeriod time.Duration
}

type configOption func(cn *config)

func MinPoolConns(minConns int32) func(cn *config) {
	return func(c *config) {
		c.poolMinConns = minConns
	}
}

func MaxPoolConns(maxConns int32) func(cn *config) {
	return func(c *config) {
		c.poolMaxConns = maxConns
	}
}

func newConfig() config {
	return config{
		sslMode:               "disable",
		timeZone:              "UTC",
		poolMaxConns:          4,
		poolMinConns:          0,
		poolMaxConnLifetime:   time.Hour,
		poolMaxConnIdleTime:   time.Minute * 30,
		poolHealthCheckPeriod: time.Minute,
	}
}

func formatOption(url, option string, value interface{}) string {
	return url + " " + option + "=" + fmt.Sprintf("%v", value)
}

func addOptionsToUrl(url string, options ...configOption) string {
	config := newConfig()
	for _, option := range options {
		option(&config)
	}

	url = formatOption(url, "sslmode", config.sslMode)
	url = formatOption(url, "TimeZone", config.timeZone)
	url = formatOption(url, "pool_max_conns", config.poolMaxConns)
	url = formatOption(url, "pool_min_conns", config.poolMinConns)
	url = formatOption(url, "pool_max_conn_lifetime", config.poolMaxConnLifetime)
	url = formatOption(url, "pool_max_conn_idle_time", config.poolMaxConnIdleTime)
	url = formatOption(url, "pool_health_check_period", config.poolHealthCheckPeriod)

	return url
}

func ConnectDb(ctx context.Context, url string, options ...configOption) (*pgxpool.Pool, error) {
	url = addOptionsToUrl(url, options...)
	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}

	poolConfig.ConnConfig.Logger = logger{}

	pool, err := pgxpool.ConnectConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

type logger struct {
}

func (l logger) Log(ctx context.Context, level pgx.LogLevel, msg string, data map[string]interface{}) {
	var evt *zerolog.Event
	switch level {
	case pgx.LogLevelTrace:
		evt = log.Trace()
	case pgx.LogLevelDebug:
		evt = log.Debug()
	case pgx.LogLevelInfo:
		evt = log.Info()
	case pgx.LogLevelWarn:
		evt = log.Warn()
	case pgx.LogLevelError:
		evt = log.Error()
	case pgx.LogLevelNone:
		evt = log.Info()
	default:
		evt = log.Info()
	}

	for k, v := range data {
		evt.Interface(k, v)
	}

	evt.Msg(msg)
}

func RunMigrations(host, database, port, user, password string, clean bool) error {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, host, port, database)
	m, err := migrate.New("file:db/migrations", connStr)
	if err != nil {
		m, err = migrate.New("file:db/migrations", connStr)
		if err != nil {
			return err
		}
	}
	if clean {
		if err := m.Down(); err != nil {
			if err != migrate.ErrNoChange {
				return err
			}
		}
	}
	if err := m.Up(); err != nil {
		if err != migrate.ErrNoChange {
			return err
		}
		log.Info().Msg("schema is up to date")
	}

	return nil
}
