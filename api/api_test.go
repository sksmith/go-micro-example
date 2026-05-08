package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/testutil"
)

func TestMain(m *testing.M) {
	testutil.ConfigLogging()
	os.Exit(m.Run())
}

func newTestRouter() (chi.Router, *user.MockUserService) {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	return api.ConfigureRouter(cfg, invSvc, resSvc, usrSvc), usrSvc
}

func TestCorsConfig(t *testing.T) {
	tests := []struct {
		origin string
		want   string
	}{
		{origin: "https://evilorigin.com", want: ""},
		{origin: "http://evilorigin.com", want: ""},
		{origin: "https://subdomain.seanksmith.me", want: "https://subdomain.seanksmith.me"},
		{origin: "http://subdomain.seanksmith.me", want: "http://subdomain.seanksmith.me"},
		{origin: "http://subdomain.seanksmith.evil.me", want: ""},
		{origin: "http://localhost:8080", want: "http://localhost:8080"},
		{origin: "http://localhost:3000", want: "http://localhost:3000"},
		{origin: "https://localhost:8080", want: "https://localhost:8080"},
		{origin: "https://localhost:3000", want: "https://localhost:3000"},
		{origin: "https://localhostevil:3000", want: ""},
	}

	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	client := http.DefaultClient
	url := ts.URL + api.ApiPath + api.InventoryPath

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
			t.Errorf("failed cors test got=[%v] want=[%v]", got, test.want)
		}
	}
}

func TestApiRoutesRequireAuthentication(t *testing.T) {
	r, usrSvc := newTestRouter()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		return user.User{}, core.ErrNotFound
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	tests := []struct {
		name string
		path string
	}{
		{"inventory", api.ApiPath + api.InventoryPath},
		{"reservation", api.ApiPath + api.ReservationPath},
		{"user", api.ApiPath + api.UserPath},
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

		t.Run("bad-credentials/"+test.name, func(t *testing.T) {
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
				t.Errorf("expected 401 for bad-credentials %s, got %d", test.path, res.StatusCode)
			}
		})
	}
}

func TestUnauthenticatedEndpointsRemainOpen(t *testing.T) {
	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	openPaths := []string{api.HealthEndpoint, api.MetricsEndpoint}
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
