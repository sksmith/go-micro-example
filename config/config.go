package config

import (
	"flag"
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
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
	port         *string
	configSource *string
	configUrl    *string
	configBranch *string
	configUser   *string
	configPass   *string
)

type StringConfig struct {
	Value       string `json:"value"   yaml:"value"`
	Default     string `json:"default" yaml:"default"`
	Description string `json:"description" yaml:"description"`
}

type BoolConfig struct {
	Value       bool   `json:"value"   yaml:"value"`
	Default     bool   `json:"default" yaml:"default"`
	Description string `json:"description" yaml:"description"`
}

type IntConfig struct {
	Value       int64  `json:"value"   yaml:"value"`
	Default     int64  `json:"default" yaml:"default"`
	Description string `json:"description" yaml:"description"`
}

type Config struct {
	AppName     StringConfig `json:"appName"     yaml:"appName"`
	AppVersion  StringConfig `json:"appVersion"  yaml:"appVersion"`
	Sha1Version StringConfig `json:"sha1Version" yaml:"sha1Version"`
	BuildTime   StringConfig `json:"buildTime"   yaml:"buildTime"`
	Profile     StringConfig `json:"profile"     yaml:"profile"`
	Revision    StringConfig `json:"revision"    yaml:"revision"`
	Port        StringConfig `json:"port"        yaml:"port"`
	Config      ConfigSource `json:"config"      yaml:"config"`
	Log         LogConfig    `json:"log"         yaml:"log"`
	Db          DbConfig     `json:"db"          yaml:"db"`
	RabbitMQ    QueueConfig  `json:"rabbitmq"    yaml:"rabbitmq"`
}

type ConfigSource struct {
	Print       BoolConfig   `json:"print"  yaml:"print"`
	Source      StringConfig `json:"source" yaml:"source"`
	Spring      SpringConfig `json:"spring" yaml:"spring"`
	Description string       `json:"description" yaml:"description"`
}

type SpringConfig struct {
	Url         StringConfig `json:"url"    yaml:"url"`
	Branch      StringConfig `json:"branch" yaml:"branch"`
	User        StringConfig `json:"user"   yaml:"user"`
	Pass        StringConfig `json:"pass"   yaml:"pass"`
	Description string       `json:"description" yaml:"description"`
}

type LogConfig struct {
	Level       StringConfig `json:"level"      yaml:"level"`
	Structured  BoolConfig   `json:"structured" yaml:"structured"`
	Description string       `json:"description" yaml:"description"`
}

type DbConfig struct {
	Name        StringConfig `json:"name"     yaml:"name"`
	Host        StringConfig `json:"host"     yaml:"host"`
	Port        StringConfig `json:"port"     yaml:"port"`
	Migrate     BoolConfig   `json:"migrate"  yaml:"migrate"`
	Clean       BoolConfig   `json:"clean"    yaml:"clean"`
	InMemory    BoolConfig   `json:"inMemory" yaml:"inMemory"`
	User        StringConfig `json:"user"     yaml:"user"`
	Pass        StringConfig `json:"pass"     yaml:"pass"`
	Pool        DbPoolConfig `json:"pool"     yaml:"pool"`
	LogLevel    StringConfig `json:"logLevel" yaml:"logLevel"`
	Description string       `json:"description" yaml:"description"`
}

type DbPoolConfig struct {
	MinSize           IntConfig `json:"minPoolSize"       yaml:"minPoolSize"`
	MaxSize           IntConfig `json:"maxPoolSize"       yaml:"maxPoolSize"`
	MaxConnLife       IntConfig `json:"maxConnLife"       yaml:"maxConnLife"`
	MaxConnIdle       IntConfig `json:"maxConnIdle"       yaml:"maxConnIdle"`
	HealthCheckPeriod IntConfig `json:"healthCheckPeriod" yaml:"healthCheckPeriod"`
	Description       string    `json:"description" yaml:"description"`
}

type QueueConfig struct {
	Host        StringConfig           `json:"host"        yaml:"host"`
	Port        StringConfig           `json:"port"        yaml:"port"`
	User        StringConfig           `json:"user"        yaml:"user"`
	Pass        StringConfig           `json:"pass"        yaml:"pass"`
	Mock        BoolConfig             `json:"mock"        yaml:"mock"`
	Inventory   InventoryQueueConfig   `json:"inventory"   yaml:"inventory"`
	Reservation ReservationQueueConfig `json:"reservation" yaml:"reservation"`
	Product     ProductQueueConfig     `json:"product"     yaml:"product"`
	Description string                 `json:"description" yaml:"description"`
}

type InventoryQueueConfig struct {
	Exchange    StringConfig `json:"exchange" yaml:"exchange"`
	Description string       `json:"description" yaml:"description"`
}

type ReservationQueueConfig struct {
	Exchange    StringConfig `json:"exchange" yaml:"exchange"`
	Description string       `json:"description" yaml:"description"`
}

type ProductQueueConfig struct {
	Queue       StringConfig          `json:"queue" yaml:"queue"`
	Dlt         ProductQueueDltConfig `json:"dlt"   yaml:"dlt"`
	Description string                `json:"description" yaml:"description"`
}

type ProductQueueDltConfig struct {
	Exchange    StringConfig `json:"exchange" yaml:"exchange"`
	Description string       `json:"description" yaml:"description"`
}

func (c *Config) Print() {
	if c.Config.Print.Value {
		log.Info().Interface("config", c).Msg("the following configurations have successfully loaded")
	}
}

func init() {
	def := &Config{}
	setupDefaults(def)

	profile = flag.String("p", def.Profile.Default, def.Profile.Description)
	port = flag.String("port", def.Port.Default, def.Port.Description)
	configSource = flag.String("s", def.Config.Source.Default, def.Config.Source.Description)
	configUrl = flag.String("cfgUrl", def.Config.Spring.Url.Default, def.Config.Spring.Url.Description)
	configBranch = flag.String("cfgBranch", def.Config.Spring.Branch.Default, def.Config.Spring.Branch.Description)
	configUser = flag.String("cfgUser", def.Config.Spring.User.Default, def.Config.Spring.User.Description)
	configPass = flag.String("cfgPass", def.Config.Spring.Pass.Default, def.Config.Spring.Pass.Description)

	viper.SetDefault("port", def.Port.Default)
	viper.SetDefault("profile", def.Profile.Default)

	viper.SetDefault("config.print", def.Config.Print.Default)
	viper.SetDefault("config.source", def.Config.Source.Default)

	viper.SetDefault("log.level", def.Log.Level.Default)
	viper.SetDefault("log.structured", def.Log.Structured.Default)

	viper.SetDefault("db.name", def.Db.Name.Default)
	viper.SetDefault("db.host", def.Db.Host.Default)
	viper.SetDefault("db.port", def.Db.Port.Default)
	viper.SetDefault("db.user", def.Db.User.Default)
	viper.SetDefault("db.pass", def.Db.Pass.Default)
	viper.SetDefault("db.clean", def.Db.Clean.Default)
	viper.SetDefault("db.inMemory", def.Db.InMemory.Default)
	viper.SetDefault("db.pool.minSize", def.Db.Pool.MinSize.Default)
	viper.SetDefault("db.pool.maxSize", def.Db.Pool.MaxSize.Default)

	viper.SetDefault("rabbitmq.host", def.RabbitMQ.Host.Default)
	viper.SetDefault("rabbitmq.port", def.RabbitMQ.Port.Default)
	viper.SetDefault("rabbitmq.user", def.RabbitMQ.User.Default)
	viper.SetDefault("rabbitmq.pass", def.RabbitMQ.Pass.Default)
	viper.SetDefault("rabbitmq.mock", def.RabbitMQ.Mock.Default)
	viper.SetDefault("rabbitmq.inventory.exchange", def.RabbitMQ.Inventory.Exchange.Default)
	viper.SetDefault("rabbitmq.reservation.exchange", def.RabbitMQ.Reservation.Exchange.Default)
	viper.SetDefault("rabbitmq.product.queue", def.RabbitMQ.Product.Queue.Default)
	viper.SetDefault("rabbitmq.product.dlt.exchange", def.RabbitMQ.Product.Dlt.Exchange.Default)
}

func LoadDefaults() *Config {
	config := &Config{}
	setupDefaults(config)
	return config
}

func Load() *Config {
	config := &Config{}
	setupDefaults(config)

	var err error
	switch config.Config.Source.Value {
	case "local":
		err = loadLocalConfigs(config)
	case "etcd":
		err = loadRemoteConfigs(config)
	default:
		log.Warn().
			Str("configSource", config.Config.Source.Value).
			Msg("unrecognized configuration source, using local")

		err = loadLocalConfigs(config)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	err = loadCommandLineOverrides(config)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}

	return config
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

	err = viper.Unmarshal(config, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		ValueToConfigValue(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)))
	if err != nil {
		return err
	}

	return nil
}

func ValueToConfigValue() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {

		if t != reflect.Struct {
			return data, nil
		}

		switch f {
		case reflect.Int:
			raw := int64(data.(int))
			return IntConfig{Value: raw}, nil
		case reflect.Int64:
			raw := data.(int64)
			return IntConfig{Value: raw}, nil
		case reflect.String:
			raw := data.(string)
			return StringConfig{Value: raw}, nil
		case reflect.Bool:
			raw := data.(bool)
			return BoolConfig{Value: raw}, nil
		}

		return data, nil
	}
}

func loadRemoteConfigs(config *Config) error {

	return nil
}

func loadCommandLineOverrides(config *Config) error {
	flag.Parse()
	if *profile != config.Profile.Default {
		config.Profile.Value = *profile
	}
	if *port != config.Port.Default {
		config.Port.Value = *port
	}
	if *configSource != config.Config.Source.Default {
		config.Config.Source.Value = *configSource
	}
	if *configUrl != config.Config.Spring.Url.Default {
		config.Config.Spring.Url.Value = *configUrl
	}
	if *configBranch != config.Config.Spring.Branch.Default {
		config.Config.Spring.Branch.Value = *configBranch
	}
	if *configUser != config.Config.Spring.User.Default {
		config.Config.Spring.User.Value = *configUser
	}
	if *configPass != config.Config.Spring.Pass.Default {
		config.Config.Spring.Pass.Value = *configPass
	}
	return nil
}

func setupDefaults(config *Config) {
	config.AppName = StringConfig{Value: AppName, Default: AppName, Description: "Name of the application in a human readable format. Example: Go Micro Example"}

	config.AppVersion = StringConfig{Value: "", Default: "", Description: "Semantic version of the application. Example: v1.2.3"}
	config.Sha1Version = StringConfig{Value: "", Default: "", Description: "Git sha1 hash of the application version."}
	config.BuildTime = StringConfig{Value: "", Default: "", Description: "When this version of the application was compiled."}
	config.Profile = StringConfig{Value: "local", Default: "local", Description: "Running profile of the application, can assist with sensible defaults or change behavior. Examples: local, dev, prod"}
	config.Revision = StringConfig{Value: Revision, Default: Revision, Description: "A hard coded revision handy for quickly determining if local changes are running. Examples: 1, Two, 9999"}
	config.Port = StringConfig{Value: "8080", Default: "8080", Description: "Port that the application will bind to on startup. Examples: 8080, 3000"}

	config.Config.Description = "Settings for where and how the application should get its configurations."
	config.Config.Print = BoolConfig{Value: false, Default: false, Description: "Print configurations on startup."}
	config.Config.Source = StringConfig{Value: "", Default: "", Description: "Where the application should go for configurations. Examples: local, etcd"}

	config.Config.Spring.Description = "Configuration settings for Spring Cloud Config. These are only used if config.source is spring."
	config.Config.Spring.Url = StringConfig{Value: "", Default: "", Description: "The url of the Spring Cloud Config server."}
	config.Config.Spring.Branch = StringConfig{Value: "", Default: "", Description: "The git branch to use to pull configurations from. Examples: main, master, development"}
	config.Config.Spring.User = StringConfig{Value: "", Default: "", Description: "User to use when connecting to the Spring Cloud Config server."}
	config.Config.Spring.Pass = StringConfig{Value: "", Default: "", Description: "Password to use when connecting to the Spring Cloud Config server."}

	config.Log.Description = "Settings for applicaton logging."
	config.Log.Level = StringConfig{Value: "trace", Default: "trace", Description: "The lowest level that the application should log at. Examples: info, warn, error."}
	config.Log.Structured = BoolConfig{Value: false, Default: false, Description: "Whether the application should output structured (json) logging, or human friendly plain text."}

	config.Db.Description = "Database configurations."
	config.Db.Name = StringConfig{Value: "micro-ex-db", Default: "micro-ex-db", Description: "The name of the database to connect to."}
	config.Db.Host = StringConfig{Value: "5432", Default: "5432", Description: "Port of the database."}
	config.Db.Migrate = BoolConfig{Value: true, Default: true, Description: "Whether or not database migrations should be executed on startup."}
	config.Db.Clean = BoolConfig{Value: false, Default: false, Description: "WARNING: THIS WILL DELETE ALL DATA FROM THE DB. Used only during migration. If clean is true, all 'down' migrations are executed."}
	config.Db.InMemory = BoolConfig{Value: false, Default: false, Description: "Whether or not the application should use an in memory database."}
	config.Db.User = StringConfig{Value: "postgres", Default: "postgres", Description: "User the application will use to connect to the database."}
	config.Db.Pass = StringConfig{Value: "postgres", Default: "postgres", Description: "Password the application will use for connecting to the database."}
	config.Db.Pool.MinSize = IntConfig{Value: 1, Default: 1, Description: "The minimum size of the pool."}
	config.Db.Pool.MaxSize = IntConfig{Value: 3, Default: 3, Description: "The maximum size of the pool."}
	config.Db.Pool.MaxConnLife = IntConfig{Value: time.Hour.Milliseconds(), Default: time.Hour.Milliseconds(), Description: "The maximum time a connection can live in the pool in milliseconds."}
	config.Db.Pool.MaxConnIdle = IntConfig{Value: time.Minute.Milliseconds() * 30, Default: time.Minute.Milliseconds() * 30, Description: "The maximum time a connection can idle in the pool in milliseconds."}
	config.Db.LogLevel = StringConfig{Value: "trace", Default: "trace", Description: "The logging level for database interactions. See: log.level"}

	config.RabbitMQ.Description = "Rabbit MQ congfigurations."
	config.RabbitMQ.Host = StringConfig{Value: "localhost", Default: "localhost", Description: "RabbitMQ's broker host."}
	config.RabbitMQ.Port = StringConfig{Value: "5432", Default: "5432", Description: "RabbitMQ's broker host port."}
	config.RabbitMQ.User = StringConfig{Value: "guest", Default: "guest", Description: "User the application will use to connect to RabbitMQ."}
	config.RabbitMQ.Pass = StringConfig{Value: "guest", Default: "guest", Description: "Password the application will use to connect to RabbitMQ."}
	config.RabbitMQ.Mock = BoolConfig{Value: false, Default: false, Description: "Whether or not the application should mock sending messages to RabbitMQ."}

	config.RabbitMQ.Inventory.Description = "RabbitMQ settings for inventory related updates."
	config.RabbitMQ.Inventory.Exchange = StringConfig{Value: "inventory.exchange", Default: "inventory.exchange", Description: "RabbitMQ exchang}}e to use for posting inventory updates."}

	config.RabbitMQ.Reservation.Description = "RabbitMQ settings for reservation related updates."
	config.RabbitMQ.Reservation.Exchange = StringConfig{Value: "reservation.exchange", Default: "reservation.exchange", Description: "RabbitMQ exchange to use for posting reservation updates."}

	config.RabbitMQ.Product.Description = "RabbitMQ settings for product related updates."
	config.RabbitMQ.Product.Queue = StringConfig{Value: "product.queue", Default: "product.queue", Description: "Queue used for listening to product updates coming from a theoretical product management system."}

	config.RabbitMQ.Product.Dlt.Description = "Configurations for the product dead letter topic, where messages that fail to be read from the queue are written."
	config.RabbitMQ.Product.Dlt.Exchange = StringConfig{Value: "product.dlt.exchange", Default: "product.dlt.exchange", Description: "Exchange used for posting messages to the dead letter topic."}
}
