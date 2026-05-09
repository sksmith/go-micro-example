package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/testutil"
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
	testutil.Unmarshal(res, got, t)

	if got.AppName != cfg.AppName {
		t.Errorf("unexpected app name got=[%v] want=[%v]", got, cfg.AppName)
	}
}

func TestEnvEndpointRedactsSensitiveValues(t *testing.T) {
	const (
		dbPass     = "super-secret-db-password"
		rabbitPass = "super-secret-mq-password"
		dbHost     = "internal-db.prod"
		rabbitHost = "internal-mq.prod"
		dbUser     = "prod-db-user"
		rabbitUser = "prod-mq-user"
		springUrl  = "https://config.internal/prod"
		springUser = "spring-user"
		springPass = "spring-pass"
	)

	cfg := config.LoadDefaults()
	cfg.Db.Pass.Value = dbPass
	cfg.Db.Host.Value = dbHost
	cfg.Db.User.Value = dbUser
	cfg.RabbitMQ.Pass.Value = rabbitPass
	cfg.RabbitMQ.Host.Value = rabbitHost
	cfg.RabbitMQ.User.Value = rabbitUser
	cfg.Config.Spring.Url.Value = springUrl
	cfg.Config.Spring.User.Value = springUser
	cfg.Config.Spring.Pass.Value = springPass

	envApi := api.NewEnvApi(cfg)
	r := chi.NewRouter()
	envApi.ConfigureRouter(r)

	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	leaks := []string{dbPass, rabbitPass, dbHost, rabbitHost, dbUser, rabbitUser, springUrl, springUser, springPass}
	for _, leak := range leaks {
		if strings.Contains(string(body), leak) {
			t.Errorf("/env response leaked sensitive value %q", leak)
		}
	}
}

func TestEnvEndpointRequiresAdmin(t *testing.T) {
	r, usrSvc := newTestRouter()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		return user.User{Username: "regular", IsAdmin: false}, nil
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	envURL := ts.URL + api.ApiPath + api.AdminPath + api.EnvPath

	req, err := http.NewRequest(http.MethodGet, envURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("regular", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-admin user, got %d", res.StatusCode)
	}
}

func TestOldEnvPathIsGone(t *testing.T) {
	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/env")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 at unauthenticated /env, got %d", res.StatusCode)
	}
}
