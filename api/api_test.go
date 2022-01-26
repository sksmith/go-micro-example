package api_test

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
)

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

	r := getRouter()
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

func getRouter() chi.Router {
	cfg := config.LoadDefaults()
	invSvc, resSvc, usrSvc := getMocks()
	return api.ConfigureRouter(cfg, &invSvc, &resSvc, &usrSvc)
}

func getMocks() (inventory.MockInventoryService, inventory.MockReservationService, user.MockUserService) {
	return inventory.NewMockInventoryService(), inventory.NewMockReservationService(), user.NewMockUserService()
}

func unmarshal(res *http.Response, v interface{}, t *testing.T) {
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(body, v)
	if err != nil {
		t.Fatal(err)
	}
}
