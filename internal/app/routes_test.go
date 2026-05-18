package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/app"
	"github.com/sksmith/go-micro-example/internal/auth"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/testutil"
	"github.com/sksmith/go-micro-example/internal/user"
)

func TestMain(m *testing.M) {
	testutil.ConfigLogging()
	os.Exit(m.Run())
}

func newTestRouter() (chi.Router, *user.MockUserService) {
	r, usrSvc, _ := newTestRouterWithSigner()
	return r, usrSvc
}

func newTestRouterWithSigner() (chi.Router, *user.MockUserService, *auth.Signer) {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		panic(err)
	}
	return app.ConfigureRouter(cfg, invSvc, resSvc, usrSvc, signer, nil, nil, nil, nil), usrSvc, signer
}

func TestCorsConfig(t *testing.T) {
	// SEC-006: CORS now drives off an explicit comma-separated
	// exact-origin list in config.cors.allowedOrigins. Defaults allow
	// http://localhost:8080 + https://localhost; anything else — wildcards,
	// suffix attacks, look-alike hostnames — must be denied.
	tests := []struct {
		origin string
		want   string
	}{
		// Exact-match origins from the default config: reflected.
		{origin: "http://localhost:8080", want: "http://localhost:8080"},
		{origin: "https://localhost", want: "https://localhost"},

		// Unlisted origins: no Access-Control-Allow-Origin.
		{origin: "https://evilorigin.com", want: ""},
		{origin: "http://evilorigin.com", want: ""},

		// Old wildcarded entries (https://*.seanksmith.me, http://localhost:*)
		// must no longer match anything.
		{origin: "https://subdomain.seanksmith.me", want: ""},
		{origin: "http://subdomain.seanksmith.me", want: ""},
		{origin: "http://localhost:3000", want: ""},
		{origin: "https://localhost:8080", want: ""},

		// Look-alike host that prefix-matched the old localhost wildcard.
		{origin: "https://localhostevil:3000", want: ""},
	}

	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	client := http.DefaultClient
	url := ts.URL + app.ApiPath + app.InventoryPath

	for _, test := range tests {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Add("Origin", test.origin)

		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		got := res.Header.Get("Access-Control-Allow-Origin")
		if got != test.want {
			t.Errorf("failed cors test origin=%q got=[%v] want=[%v]", test.origin, got, test.want)
		}
	}
}

func TestCorsDisabledWhenAllowedOriginsEmpty(t *testing.T) {
	// SEC-006: empty config.cors.allowedOrigins must skip the CORS
	// middleware entirely — no Access-Control-Allow-Origin header on
	// any request, regardless of the Origin sent.
	cfg := config.LoadDefaults()
	cfg.CORS.AllowedOrigins.Value = ""
	invSvc, resSvc, usrSvc := getMocks()
	signer, err := auth.NewSigner(nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	r := app.ConfigureRouter(cfg, invSvc, resSvc, usrSvc, signer, nil, nil, nil, nil)
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+app.ApiPath+app.InventoryPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Origin", "http://localhost:8080")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin when CORS disabled, got %q", got)
	}
}

func TestApiRoutesRequireAuthentication(t *testing.T) {
	r, usrSvc := newTestRouter()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		return user.User{}, user.ErrInvalidCredentials
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	tests := []struct {
		name string
		path string
	}{
		{"inventory", app.ApiPath + app.InventoryPath},
		{"reservation", app.ApiPath + app.ReservationPath},
		{"user", app.ApiPath + app.UserPath},
		{"admin/env", app.ApiPath + app.AdminPath + app.EnvPath},
	}

	for _, test := range tests {
		t.Run("no-credentials/"+test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 for unauthenticated %s, got %d", test.path, res.StatusCode)
			}
		})

		t.Run("basic-auth-rejected/"+test.name, func(t *testing.T) {
			// SEC-002c: Basic Auth is no longer accepted on protected routes.
			req, err := http.NewRequest(http.MethodGet, ts.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.SetBasicAuth("nobody", "wrong")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 for Basic-Auth attempt on %s, got %d", test.path, res.StatusCode)
			}
			if got := res.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("expected WWW-Authenticate: Bearer on %s, got %q", test.path, got)
			}
		})
	}
}

func TestUnauthenticatedEndpointsRemainOpen(t *testing.T) {
	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	openPaths := []string{app.LivenessEndpoint, app.ReadinessEndpoint, app.MetricsEndpoint}
	for _, p := range openPaths {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			t.Errorf("expected %s to be reachable without auth, got 401", p)
		}
	}
}

func getMocks() (*inventory.MockInventoryService, *inventory.MockReservationService, *user.MockUserService) {
	return inventory.NewMockInventoryService(), inventory.NewMockReservationService(), user.NewMockUserService()
}
