package config

import (
	"flag"
	"os"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

const (
	AppName  = "Go Micro Example"
	Revision = "1"
)

var (
	// Build time arguments
	AppVersion  string
	Sha1Version string
	BuildTime   string
)

// https://github.com/spf13/viper#unmarshaling
type Config struct {
	AppName     string `json:"appName"`
	AppVersion  string `json:"appVersion"`
	Sha1Version string `json:"sha1Version"`
	BuildTime   string `json:"buildTime"`
	Profile     string `json:"profile"`
	Revision    string `json:"revision"`
	Port        string `json:"port"`
	Source      string `json:"configSource"`
	Branch      string `json:"configBranch"`
	Log         struct {
		Level      string
		Structured bool
	}
	Db struct {
		Name     string
		Host     string
		Port     string
		User     string
		Pass     string
		Migrate  bool
		Clean    bool
		InMemory bool
	} `json:"db"`
	RabbitMQ struct {
		Host      string
		Port      string
		User      string
		Pass      string
		Mock      bool
		Inventory struct {
			Exchange string
		}
		Reservation struct {
			Exchange string
		}
		Product struct {
			Queue string
			Dlt   struct {
				Exchange string
			}
		}
	}
}

var (
	// Runtime flags
	profile *string
)

func init() {
	profile = flag.String("p", "local", "profile for the application config")

	viper.SetDefault("port", "8080")
	viper.SetDefault("profile", "local")

	viper.SetDefault("log.level", "trace")
	viper.SetDefault("log.structured", false)

	viper.SetDefault("db.name", "micro-tmpl-db")
	viper.SetDefault("db.host", "localhost")
	viper.SetDefault("db.port", "5432")
	viper.SetDefault("db.user", "postgres")
	viper.SetDefault("db.pass", "postgres")
	viper.SetDefault("db.clean", false)
	viper.SetDefault("db.inMemory", false)

	viper.SetDefault("rabbitmq.host", "localhost")
	viper.SetDefault("rabbitmq.port", "5672")
	viper.SetDefault("rabbitmq.user", "guest")
	viper.SetDefault("rabbitmq.pass", "guest")
	viper.SetDefault("rabbitmq.mock", false)
	viper.SetDefault("rabbitmq.inventory.exchange", "inventory.exchange")
	viper.SetDefault("rabbitmq.reservation.exchange", "reservation.exchange")
	viper.SetDefault("rabbitmq.product.queue", "product.queue")
	viper.SetDefault("rabbitmq.product.dlt.exchange", "product.dlt.exchange")
}

var (
	configUrl    = os.Getenv("CONFIG_SERVER_URL")
	configBranch = os.Getenv("CONFIG_SERVER_BRANCH")
	configUser   = os.Getenv("CONFIG_SERVER_USER")
	configPass   = os.Getenv("CONFIG_SERVER_PASS")
	printConfigs = getPrintConfigs()
)

func getPrintConfigs() bool {
	v, err := strconv.ParseBool(os.Getenv("PRINT_CONFIGS"))
	if err != nil {
		return false
	}
	return v
}

func Load() (config *Config) {
	var err error

	if *profile == "local" || *profile == "" {
		log.Info().Msg("loading local configurations...")
		config, err = loadLocalConfigs()
	} else {
		config, err = loadRemoteConfigs(configUrl, configBranch, configUser, configPass, *profile)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	config.AppName = AppName
	config.Profile = *profile
	config.Revision = Revision

	return config
}

func loadLocalConfigs() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}

	appConfig := &Config{}

	err = viper.Unmarshal(appConfig)
	if err != nil {
		return nil, err
	}

	appConfig.Source = "local"
	appConfig.Branch = "n/a"

	return appConfig, nil
}

func loadRemoteConfigs(configUrl, configBranch, configUser, configPass, profile string) (*Config, error) {

	return nil, nil
}
