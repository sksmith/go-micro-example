package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db"
	"github.com/sksmith/go-micro-example/db/invrepo"
	"github.com/sksmith/go-micro-example/db/usrrepo"
	"github.com/sksmith/go-micro-example/queue"
	"github.com/sksmith/go-micro-example/testutil"
)

var cfg *config.Config

func TestMain(m *testing.M) {

	log.Info().Msg("configuring logging...")

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx := context.Background()
	cfg = config.Load("config_test")

	level, err := zerolog.ParseLevel(cfg.Log.Level.Value)
	if err != nil {
		log.Fatal().Err(err)
	}
	zerolog.SetGlobalLevel(level)
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	cfg.Print()

	dbPool, err := db.ConnectDb(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to db")
	}

	iq := queue.NewInventoryQueue(ctx, cfg)

	ir := invrepo.NewPostgresRepo(dbPool)

	invService := inventory.NewService(ir, iq)

	ur := usrrepo.NewPostgresRepo(dbPool)

	userService := user.NewService(ur)

	r := api.ConfigureRouter(cfg, invService, invService, userService)

	_ = queue.NewProductQueue(ctx, cfg, invService)

	go func() {
		log.Fatal().Err(http.ListenAndServe(":"+cfg.Port.Value, r))
	}()

	waitForReady()
	os.Exit(m.Run())
}

func waitForReady() {
	for {
		res, err := http.Get(host() + "/health")
		if err == nil && res.StatusCode == 200 {
			break
		}
		log.Info().Msg("application not ready, sleeping")
		time.Sleep(1 * time.Second)
	}
}

func TestCreateProduct(t *testing.T) {
	cases := []struct {
		name    string
		product inventory.Product

		wantSku        string
		wantStatusCode int
	}{
		{
			name:           "valid request",
			product:        inventory.Product{Sku: "somesku", Upc: "someupc", Name: "somename"},
			wantSku:        "somesku",
			wantStatusCode: 201,
		},
		{
			name:           "valid request with a long name",
			product:        inventory.Product{Sku: "someskuwithareallylongname", Upc: "longskuupc", Name: "somename"},
			wantSku:        "someskuwithareallylongname",
			wantStatusCode: 201,
		},
		{
			name:           "missing sku",
			product:        inventory.Product{Sku: "", Upc: "skurequiredupc", Name: "skurequiredname"},
			wantStatusCode: 400,
		},
		{
			name:           "missing upc",
			product:        inventory.Product{Sku: "upcreqsku", Upc: "", Name: "upcreqname"},
			wantStatusCode: 400,
		},
		{
			name:           "missing name",
			product:        inventory.Product{Sku: "namereqsku", Upc: "namerequpc", Name: ""},
			wantStatusCode: 400,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := api.CreateProductRequest{Product: test.product}
			res := testutil.Put(host()+"/api/v1/inventory", request, t)

			if res.StatusCode != test.wantStatusCode {
				t.Errorf("unexpected status got=%d want=%d", res.StatusCode, test.wantStatusCode)
			}

			body := &api.ProductResponse{}
			testutil.Unmarshal(res, body, t)
			if test.wantSku != "" && body.Sku != test.wantSku {
				t.Errorf("unexpected response sku got=%s want=%s", body.Sku, test.wantSku)
			}
		})
	}
}

func TestList(t *testing.T) {
	cases := []struct {
		name string
		url  string

		wantMinRespLen int
		wantStatusCode int
	}{
		{
			name:           "valid request",
			url:            "/api/v1/inventory",
			wantMinRespLen: 2,
			wantStatusCode: 200,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			res, err := http.Get(host() + test.url)

			if err != nil {
				t.Errorf("unexpected error got=%s", err)
			}
			if res.StatusCode != test.wantStatusCode {
				t.Errorf("unexpected status got=%d want=%d", res.StatusCode, test.wantStatusCode)
			}

			body := []inventory.ProductInventory{}
			testutil.Unmarshal(res, &body, t)
			if len(body) < test.wantMinRespLen {
				t.Errorf("unexpected response len got=%d want=%d", len(body), test.wantMinRespLen)
			}
		})
	}
}

func host() string {
	return "http://localhost:" + cfg.Port.Value
}
