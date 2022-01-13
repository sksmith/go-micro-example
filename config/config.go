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

type Config struct {
	AppName         string       `json:"appName"         yaml:"appName"`
	AppNameDesc     string       `json:"appNameDesc"     yaml:"appNameDesc"`
	AppVersion      string       `json:"appVersion"      yaml:"appVersion"`
	AppVersionDesc  string       `json:"appVersionDesc"  yaml:"appVersionDesc"`
	Sha1Version     string       `json:"sha1Version"     yaml:"sha1Version"`
	Sha1VersionDesc string       `json:"sha1VersionDesc" yaml:"sha1VersionDesc"`
	BuildTime       string       `json:"buildTime"       yaml:"buildTime"`
	BuildTimeDesc   string       `json:"buildTimeDesc"   yaml:"buildTimeDesc"`
	Profile         string       `json:"profile"         yaml:"profile"`
	ProfileDesc     string       `json:"profileDesc"     yaml:"profileDesc"`
	Revision        string       `json:"revision"        yaml:"revision"`
	RevisionDesc    string       `json:"revisionDesc"    yaml:"revisionDesc"`
	Port            string       `json:"port"            yaml:"port"`
	PortDesc        string       `json:"portDesc"        yaml:"portDesc"`
	Config          ConfigSource `json:"config"          yaml:"config"`
	ConfigDesc      string       `json:"configDesc"      yaml:"configDesc"`
	Log             LogConfig    `json:"log"             yaml:"log"`
	LogDesc         string       `json:"logDesc"         yaml:"logDesc"`
	Db              DbConfig     `json:"db"              yaml:"db"`
	DbDesc          string       `json:"dbDesc"          yaml:"dbDesc"`
	RabbitMQ        QueueConfig  `json:"rabbitmq"        yaml:"rabbitmq"`
	RabbitMQDesc    string       `json:"rabbitmqDesc"    yaml:"rabbitmqDesc"`
}

type ConfigSource struct {
	Print      bool         `json:"print"      yaml:"print"`
	PrintDesc  string       `json:"printDesc"  yaml:"printDesc"`
	Source     string       `json:"source"     yaml:"source"`
	SourceDesc string       `json:"sourceDesc" yaml:"source"`
	Spring     SpringConfig `json:"spring"     yaml:"spring"`
	SpringDesc string       `json:"springDesc" yaml:"springDesc"`
}

type SpringConfig struct {
	Url        string `json:"url"        yaml:"url"`
	UrlDesc    string `json:"urlDesc"    yaml:"urlDesc"`
	Branch     string `json:"branch"     yaml:"branch"`
	BranchDesc string `json:"branchDesc" yaml:"branchDesc"`
	User       string `json:"user"       yaml:"user"`
	UserDesc   string `json:"userDesc"   yaml:"userDesc"`
	Pass       string `json:"pass"       yaml:"pass"`
	PassDesc   string `json:"passDesc"   yaml:"passDesc"`
}

type LogConfig struct {
	Level          string `json:"level"      yaml:"level"`
	LevelDesc      string `json:"levelDesc"      yaml:"levelDesc"`
	Structured     bool   `json:"structured" yaml:"structured"`
	StructuredDesc string `json:"structuredDesc" yaml:"structuredDesc"`
}

type DbConfig struct {
	Name         string `json:"name"         yaml:"name"`
	NameDesc     string `json:"nameDesc"     yaml:"nameDesc"`
	Host         string `json:"host"         yaml:"host"`
	HostDesc     string `json:"hostDesc"     yaml:"hostDesc"`
	Port         string `json:"port"         yaml:"port"`
	PortDesc     string `json:"portDesc"     yaml:"portDesc"`
	Migrate      bool   `json:"migrate"      yaml:"migrate"`
	MigrateDesc  string `json:"migrateDesc"  yaml:"migrateDesc"`
	Clean        bool   `json:"clean"        yaml:"clean"`
	CleanDesc    string `json:"cleanDesc"    yaml:"cleanDesc"`
	InMemory     bool   `json:"inMemory"     yaml:"inMemory"`
	InMemoryDesc string `json:"inMemoryDesc" yaml:"inMemoryDesc"`
	User         string `json:"user"         yaml:"user"`
	UserDesc     string `json:"userDesc"     yaml:"userDesc"`
	Pass         string `json:"pass"         yaml:"pass"`
	PassDesc     string `json:"passDesc"     yaml:"passDesc"`
}

type QueueConfig struct {
	Host            string                 `json:"host"            yaml:"host"`
	HostDesc        string                 `json:"hostDesc"        yaml:"hostDesc"`
	Port            string                 `json:"port"            yaml:"port"`
	PortDesc        string                 `json:"portDesc"        yaml:"portDesc"`
	User            string                 `json:"user"            yaml:"user"`
	UserDesc        string                 `json:"userDesc"        yaml:"userDesc"`
	Pass            string                 `json:"pass"            yaml:"pass"`
	PassDesc        string                 `json:"passDesc"        yaml:"passDesc"`
	Mock            bool                   `json:"mock"            yaml:"mock"`
	MockDesc        string                 `json:"mockDesc"        yaml:"mockDesc"`
	Inventory       InventoryQueueConfig   `json:"inventory"       yaml:"inventory"`
	InventoryDesc   string                 `json:"inventoryDesc"   yaml:"inventoryDesc"`
	Reservation     ReservationQueueConfig `json:"reservation"     yaml:"reservation"`
	ReservationDesc string                 `json:"reservationDesc" yaml:"reservationDesc"`
	Product         ProductQueueConfig     `json:"product"         yaml:"product"`
	ProductDesc     string                 `json:"productDesc"     yaml:"productDesc"`
}

type InventoryQueueConfig struct {
	Exchange     string `json:"exchange" yaml:"exchange"`
	ExchangeDesc string `json:"exchangeDesc" yaml:"exchangeDesc"`
}

type ReservationQueueConfig struct {
	Exchange     string `json:"exchange" yaml:"exchange"`
	ExchangeDesc string `json:"exchangeDesc" yaml:"exchangeDesc"`
}

type ProductQueueConfig struct {
	Queue     string                `json:"queue" yaml:"queue"`
	QueueDesc string                `json:"queueDesc" yaml:"queueDesc"`
	Dlt       ProductQueueDltConfig `json:"dlt" yaml:"dlt"`
	DltDesc   string                `json:"dltDesc" yaml:"dltDesc"`
}

type ProductQueueDltConfig struct {
	Exchange     string `json:"exchange" yaml:"exchange"`
	ExchangeDesc string `json:"exchangeDesc" yaml:"exchangeDesc"`
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

	// TODO Gotta do this somewhere else
	// flag.Parse()

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
	setDescriptions(config)

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

func setDescriptions(config *Config) {
	config.AppNameDesc = "Name of the application in a human readable format. Example: Go Micro Example"
	config.AppVersionDesc = "Semantic version of the application. Example: v1.2.3"
	config.Sha1VersionDesc = "Git sha1 hash of the application version."
	config.BuildTimeDesc = "How long the application took to compile."
	config.ProfileDesc = "Running profile of the application, can assist with sensible defaults or change behavior. Examples: local, dev, prod"
	config.RevisionDesc = "A hard coded revision handy for quickly determining if local changes are running. Examples: 1, Two, 9999"
	config.PortDesc = "Port that the application will bind to on startup. Examples: 8080, 3000"
	config.ConfigDesc = "Settings for where and how the application should get its configurations."
	config.LogDesc = "Settings for applicaton logging."
	config.DbDesc = "Database configurations."
	config.RabbitMQDesc = "Rabbit MQ congfigurations."

	config.Config.PrintDesc = "Print configurations on startup."
	config.Config.SourceDesc = "Where the application should go for configurations. Examples: local, etcd"
	config.Config.SpringDesc = "Configuration settings for Spring Cloud Config. These are only used if config.source is spring."

	config.Config.Spring.UrlDesc = "The url of the Spring Cloud Config server."
	config.Config.Spring.BranchDesc = "The git branch to use to pull configurations from. Examples: main, master, development"
	config.Config.Spring.UserDesc = "User to use when connecting to the Spring Cloud Config server."
	config.Config.Spring.PassDesc = "Password to use when connecting to the Spring Cloud Config server."

	config.Log.LevelDesc = "The lowest level that the application should log at. Examples: info, warn, error."
	config.Log.StructuredDesc = "Whether the application should output structured (json) logging, or human friendly plain text."

	config.Db.NameDesc = "The name of the database to connect to."
	config.Db.HostDesc = "Host of the database."
	config.Db.PortDesc = "Port of the database."
	config.Db.MigrateDesc = "Whether or not database migrations should be executed on startup."
	config.Db.CleanDesc = "WARNING: THIS WILL DELETE ALL DATA FROM THE DB. Used only during migration. If clean is true, all 'down' migrations are executed."
	config.Db.InMemoryDesc = "Whether or not the application should use an in memory database."
	config.Db.UserDesc = "User the application will use to connect to the database."
	config.Db.PassDesc = "Password the application will use for connecting to the database."

	config.RabbitMQ.HostDesc = "RabbitMQ's broker host."
	config.RabbitMQ.PortDesc = "RabbitMQ's broker host port."
	config.RabbitMQ.UserDesc = "User the application will use to connect to RabbitMQ."
	config.RabbitMQ.PassDesc = "Password the application will use to connect to RabbitMQ."
	config.RabbitMQ.MockDesc = "Whether or not the application should mock sending messages to RabbitMQ."
	config.RabbitMQ.InventoryDesc = "RabbitMQ settings for inventory related updates."
	config.RabbitMQ.ReservationDesc = "RabbitMQ settings for reservation related updates."
	config.RabbitMQ.ProductDesc = "RabbitMQ settings for product related updates."
	config.RabbitMQ.Inventory.ExchangeDesc = "RabbitMQ exchange to use for posting inventory updates."
	config.RabbitMQ.Reservation.ExchangeDesc = "RabbitMQ exchange to use for posting reservation updates."
	config.RabbitMQ.Product.QueueDesc = "Queue used for listening to product updates coming from a theoretical product management system."
	config.RabbitMQ.Product.DltDesc = "Configurations for the product dead letter topic, where messages that fail to be read from the queue are written."
	config.RabbitMQ.Product.Dlt.ExchangeDesc = "Exchange used for posting messages to the dead letter topic."
}
