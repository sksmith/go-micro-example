package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
)

func TestGetEnvironment(t *testing.T) {
	cfg := config.LoadDefaults()
	envApi := api.NewEnvApi(cfg)
	r := chi.NewRouter()
	envApi.ConfigureRouter(r)

	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}

	got := &config.Config{}
	unmarshal(res, got, t)

	if got.AppName != cfg.AppName {
		t.Errorf("unexpected app name got=[%v] want=[%v]", got, cfg.AppName)
	}
}
