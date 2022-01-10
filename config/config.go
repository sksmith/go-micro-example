package config

import (
	"flag"

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

	// Runtime flags
	profile      *string
	configSource *string
	configUrl    *string
	configBranch *string
	configUser   *string
	configPass   *string
)

// https://github.com/spf13/viper#unmarshaling
type Config struct {
	AppName     string `json:"appName"       yaml:"appName"      description:"Name of the application in a human readable format. Example: Go Micro Example"`
	AppVersion  string `json:"appVersion"    yaml:"appVersion"   description:"Semantic version of the application. Example: v1.2.3"`
	Sha1Version string `json:"sha1Version"   yaml:"sha1Version"  description:"Git sha1 hash of the application version."`
	BuildTime   string `json:"buildTime"     yaml:"buildTime"    description:"How long the application took to compile."`
	Profile     string `json:"profile"       yaml:"profile"      description:"Running profile of the application, can assist with sensible defaults or change behavior. Examples: local, dev, prod"`
	Revision    string `json:"revision"      yaml:"revision"     description:"A hard coded revision handy for quickly determining if local changes are running. Examples: 1, Two, 9999"`
	Port        string `json:"port"          yaml:"port"         description:"Port that the application will bind to on startup. Examples: 8080, 3000"`
	Config      struct {
		Print  bool   `json:"print"  yaml:"print"  description:"Print configurations on startup."`
		Source string `json:"source" yaml:"source" description:"Where the application should go for configurations. Examples: local, etcd"`
		Spring struct {
			Url    string `json:"url,omitempty"    yaml:"url,omitempty"                     description:"The url of the Spring Cloud Config server."`
			Branch string `json:"branch,omitempty" yaml:"branch,omitempty"                  description:"The git branch to use to pull configurations from. Examples: main, master, development"`
			User   string `json:"user,omitempty"   yaml:"user,omitempty"   sensitive:"true" description:"User to use when connecting to the Spring Cloud Config server."`
			Pass   string `json:"pass,omitempty"   yaml:"pass,omitempty"   sensitive:"true" description:"Password to use when connecting to the Spring Cloud Config server."`
		} `json:"spring,omitempty" yaml:"spring,omitempty" description:"Configuration settings for Spring Cloud Config. These are only used if config.source is spring."`
	} `json:"config" yaml:"config" description:"Settings for where and how the application should get its configurations."`
	Log struct {
		Level      string `json:"level"      yaml:"level"      description:"The lowest level that the application should log at. Examples: info, warn, error."`
		Structured bool   `json:"structured" yaml:"structured" description:"Whether the application should output structured (json) logging, or human friendly plain text."`
	} `json:"log" yaml:"log" description:"Settings for applicaton logging."`
	Db struct {
		Name     string `json:"name"     yaml:"name"                      description:"The name of the database to connect to."`
		Host     string `json:"host"     yaml:"host"                      description:"Host of the database."`
		Port     string `json:"port"     yaml:"port"                      description:"Port of the database."`
		Migrate  bool   `json:"migrate"  yaml:"migrate"                   description:"Whether or not database migrations should be executed on startup."`
		Clean    bool   `json:"clean"    yaml:"clean"                     description:"WARNING: THIS WILL DELETE ALL DATA FROM THE DB. Used only during migration. If clean is true, all 'down' migrations are executed."`
		InMemory bool   `json:"inMemory" yaml:"inMemory"                  description:"Whether or not the application should use an in memory database."`
		User     string `json:"user"     yaml:"user"     sensitive:"true" description:"User the application will use to connect to the database."`
		Pass     string `json:"pass"     yaml:"pass"     sensitive:"true" description:"Password the application will use for connecting to the database."`
	} `json:"db" yaml:"db" description:"Database configurations."`
	RabbitMQ struct {
		Host      string `json:"host" yaml:"host"                  description:"RabbitMQ's broker host."`
		Port      string `json:"port" yaml:"port"                  description:"RabbitMQ's broker host port."`
		User      string `json:"user" yaml:"user" sensitive:"true" description:"User the application will use to connect to RabbitMQ."`
		Pass      string `json:"pass" yaml:"pass" sensitive:"true" description:"Password the application will use to connect to RabbitMQ."`
		Mock      bool   `json:"mock" yaml:"mock"                  description:"Whether or not the application should mock sending messages to RabbitMQ."`
		Inventory struct {
			Exchange string `json:"exchange" yaml:"exchange" description:"RabbitMQ exchange to use for posting inventory updates."`
		} `json:"inventory" yaml:"inventory" description:"RabbitMQ settings for inventory related updates."`
		Reservation struct {
			Exchange string `json:"exchange" yaml:"exchange" description:"RabbitMQ exchange to use for posting reservation updates."`
		} `json:"reservation" yaml:"reservation" description:"RabbitMQ settings for reservation related updates."`
		Product struct {
			Queue string `json:"queue" yaml:"queue" description:"Queue used for listening to product updates coming from a theoretical product management system."`
			Dlt   struct {
				Exchange string `json:"exchange" yaml:"exchange" description:"Exchange used for posting messages to the dead letter topic."`
			} `json:"dlt" yaml:"dlt" description:"Configurations for the product dead letter topic, where messages that fail to be read from the queue are written."`
		} `json:"product" yaml:"product" description:"RabbitMQ settings for product related updates."`
	} `json:"rabbitmq" yaml:"rabbitmq" description:"Rabbit MQ congfigurations."`
}

func (c *Config) Print() {
	if c.Config.Print {
		log.Info().Interface("config", c).Msg("the following configurations have successfully loaded")
	}
}

func init() {
	profile = flag.String("p", "local", "profile for the application config")
	configSource = flag.String("s", "local", "where to get configurations from")
	configUrl = flag.String("cfgUrl", "", "url for application config server")
	configBranch = flag.String("cfgBranch", "", "branch to request from the configuration server (used for spring cloud config)")
	configUser = flag.String("cfgUser", "", "username to use when connecting to the application server")
	configPass = flag.String("cfgPass", "", "password to use when connecting to the application server")

	flag.Parse()

	viper.SetDefault("port", "8080")
	viper.SetDefault("profile", "local")

	viper.SetDefault("config.print", false)

	viper.SetDefault("log.level", "trace")
	viper.SetDefault("log.structured", false)
	viper.SetDefault("log.configs", false)

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

func Load() *Config {
	config, err := createConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	switch *configSource {
	case "local":
		err = loadLocalConfigs(config)
	case "etcd":
		err = loadRemoteConfigs(config)
	default:
		log.Warn().
			Str("configSource", *configSource).
			Msg("unrecognized configuration source, using local")

		err = loadLocalConfigs(config)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	return config
}

func createConfig() (config *Config, err error) {
	config = &Config{}

	config.Config.Source = *configSource

	config.Config.Spring.Url = *configUrl
	config.Config.Spring.Branch = *configBranch
	config.Config.Spring.User = *configUser
	config.Config.Spring.Pass = *configPass

	config.AppName = AppName
	config.Revision = Revision

	return config, nil
}

func loadLocalConfigs(config *Config) error {
	log.Info().Msg("loading local configurations...")

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		return err
	}

	err = viper.Unmarshal(config)
	if err != nil {
		return err
	}

	return nil
}

func loadRemoteConfigs(config *Config) error {

	return nil
}
