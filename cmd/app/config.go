package main

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	sc "github.com/sksmith/go-spring-config"
)

type Config struct {
	Port                 string
	GenerateRoutes       bool
	LogLevel             string
	LogText              bool
	InMemoryDb           bool
	DbHost               string
	DbPort               string
	DbUser               string
	DbPass               string
	DbName               string
	DbMigrate            bool
	DbClean              bool
	QMock                bool
	QHost                string
	QPort                string
	QUser                string
	QPass                string
	Revision             string
	ApplicationName      string
	QInventoryExchange   string
	QReservationExchange string
}

const maxRetries = 5

func LoadLocalConfigs() (*Config, error) {
	appConfig := &Config{ApplicationName: ApplicationName, Revision: Revision}

	// API Configs
	appConfig.Port = "8080"
	appConfig.GenerateRoutes = false

	// Log Configs
	appConfig.LogLevel = "trace"
	appConfig.LogText = true

	// DB Configs
	appConfig.DbHost = "localhost"
	appConfig.DbPort = "5432"
	appConfig.DbUser = "postgres"
	appConfig.DbPass = "postgres"
	appConfig.DbName = "micro-tmpl-db"
	appConfig.DbMigrate = true
	appConfig.DbClean   = false
	appConfig.InMemoryDb = false

	// Queue Configs
	appConfig.QMock = false
	appConfig.QHost = "localhost"
	appConfig.QPort = "5672"
	appConfig.QUser = "guest"
	appConfig.QPass = "guest"
	appConfig.QInventoryExchange = "inventory.exchange"
	appConfig.QReservationExchange = "reservation.exchange"

	return appConfig, nil
}

func LoadRemoteConfigs(url, branch, user, pass, profile string) (*Config, error) {
	appConfig := &Config{}
	var config *sc.Config
	var err error

	for tryCount := 1; tryCount < maxRetries; tryCount++ {
		config, err = sc.LoadWithCreds(url, ApplicationName, branch, user, pass, profile)
		if err == nil {
			break
		}
		log.Error().Err(err).Msg("failed to load configurations... retrying")
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		return nil, err
	}

	if printConfigs {
		log.Info().Msg("printing configurations...")
		for k, v := range config.Values {
			log.Info().Interface(k, v).Send()
		}
	}

	// API Configs
	appConfig.Port = getString(config, "app.port")
	appConfig.GenerateRoutes = getBool(config, "app.generate.routes")

	// Log Configs
	appConfig.LogLevel = getString(config, "app.log.level")
	appConfig.LogText = getBool(config, "app.log.text")

	// DB Configs
	appConfig.DbHost = getString(config, "db.host")
	appConfig.DbPort = getString(config, "db.port")
	appConfig.DbUser = getString(config, "db.user")
	appConfig.DbPass = getString(config, "db.pass")
	appConfig.DbName = getString(config, "db.name")
	appConfig.DbMigrate = getBool(config, "db.migrate")
	appConfig.DbClean = getBool(config, "db.clean")
	appConfig.InMemoryDb = getBool(config, "db.in.memory")

	// Queue Configs
	appConfig.QMock = getBool(config, "queue.mock")
	appConfig.QHost = getString(config, "queue.host")
	appConfig.QPort = getString(config, "queue.port")
	appConfig.QUser = getString(config, "queue.user")
	appConfig.QPass = getString(config, "queue.pass")
	appConfig.QInventoryExchange = getString(config, "queue.inventory.exchange")
	appConfig.QReservationExchange = getString(config, "queue.reservation.exchange")

	return appConfig, nil
}

func getBool(c *sc.Config, property string) bool {
	return c.Get(property).(bool)
}

func getString(c *sc.Config, property string) string {
	i := c.Get(property)
	switch v := i.(type) {
	case string:
		return v
	case float64:
		whole := float64(int64(v))
		if whole == v {
			return fmt.Sprintf("%.0f", v)
		} else {
			return fmt.Sprintf("%f", v)
		}
	default:
		return "unhandled type"
	}
}