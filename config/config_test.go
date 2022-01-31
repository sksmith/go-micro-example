package config_test

import (
	"testing"

	"github.com/sksmith/go-micro-example/config"
)

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
