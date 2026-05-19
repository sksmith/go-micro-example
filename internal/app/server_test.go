package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/internal/app"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/user"
)

// fakeInvService is the minimum surface required for app.Deps to be
// constructible in a test. The router mounts the inventory routes
// against this — handlers route to its methods. For tests that
// exercise specific behavior, swap individual fields on the mock.
type fakeInvService struct {
	inventory.InventoryService
	inventory.ReservationService
}

func (fakeInvService) GetAllProductInventory(_ context.Context, _, _ int) ([]inventory.ProductInventory, error) {
	return []inventory.ProductInventory{}, nil
}

// TestNewWithDepsConstructibleInTest is the DSN-028 Phase 5
// acceptance criterion: app.Server must be constructible in a unit
// test with fakes for at least one driven port, demonstrating the
// composition root is testable. The router gets exercised via
// httptest, no Postgres / Redis / Kafka involved.
func TestNewWithDepsConstructibleInTest(t *testing.T) {
	cfg := config.LoadDefaults()
	cfg.Port.Value = "0"
	cfg.AppName.Value = "test-app"

	deps := app.Deps{
		InventorySvc: fakeInvService{},
		UserService:  user.NewMockUserService(),
		// Signer nil ⇒ /api/* routes return 401 (Authenticate
		// rejects when signer == nil). That's enough to prove the
		// router is wired without forcing the test to construct a
		// JWT signer for a smoke test.
		ReadinessDeps: map[string]app.Pinger{},
	}

	srv := app.NewWithDeps(cfg, deps)
	if srv == nil {
		t.Fatal("NewWithDeps returned nil")
	}

	// Hit /live through the constructed handler — it doesn't require
	// auth and confirms the router and middleware stack are mounted.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + app.LivenessEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("/live got status %d, want 200", res.StatusCode)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != "OK" {
		t.Errorf("/live body got=%q want=OK", string(body))
	}
}
