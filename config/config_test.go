package config_test

import (
	"os"
	"testing"

	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.ConfigLogging()
	os.Exit(m.Run())
}

func TestLoadDefaults(t *testing.T) {
	cfg := config.LoadDefaults()

	if cfg.Profile.Value != cfg.Profile.Default {
		t.Errorf("profile got=%s want=%s", cfg.Profile.Value, cfg.Profile.Default)
	}
}

func TestLoad(t *testing.T) {
	cfg := config.Load("config_test")

	if cfg.Profile.Value != "test" {
		t.Errorf("profile got=%s want=%s", cfg.Profile.Value, "test")
	}
}

func TestSensitiveCredentialsLoadFromEnv(t *testing.T) {
	tests := []struct {
		envKey string
		want   string
		got    func(*config.Config) string
	}{
		{"GME_DB_USER", "env-db-user", func(c *config.Config) string { return c.Db.User.Value }},
		{"GME_DB_PASS", "env-db-pass", func(c *config.Config) string { return c.Db.Pass.Value }},
		{"GME_RABBITMQ_USER", "env-mq-user", func(c *config.Config) string { return c.RabbitMQ.User.Value }},
		{"GME_RABBITMQ_PASS", "env-mq-pass", func(c *config.Config) string { return c.RabbitMQ.Pass.Value }},
	}

	for _, tc := range tests {
		t.Run(tc.envKey, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.want)
			cfg := config.Load("config_test")
			if got := tc.got(cfg); got != tc.want {
				t.Errorf("%s: got=%q want=%q", tc.envKey, got, tc.want)
			}
		})
	}
}

func TestSensitiveCredentialsHaveNoBakedDefaults(t *testing.T) {
	cfg := config.LoadDefaults()
	for _, tc := range []struct {
		name string
		val  string
	}{
		{"Db.User", cfg.Db.User.Default},
		{"Db.Pass", cfg.Db.Pass.Default},
		{"RabbitMQ.User", cfg.RabbitMQ.User.Default},
		{"RabbitMQ.Pass", cfg.RabbitMQ.Pass.Default},
	} {
		if tc.val != "" {
			t.Errorf("%s.Default leaked %q; expected empty so the only source is env / config.local.yml", tc.name, tc.val)
		}
	}
}
