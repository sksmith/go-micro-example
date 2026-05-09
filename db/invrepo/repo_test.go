package invrepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/db/invrepo"
)

type repoT interface {
	SaveProduct(ctx context.Context, p inventory.Product, options ...core.UpdateOptions) error
	SaveProductInventory(ctx context.Context, pi inventory.ProductInventory, options ...core.UpdateOptions) error
	GetProduct(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error)
	GetProductInventory(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error)
	GetAllProductInventory(ctx context.Context, limit, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error)
	GetProductionEventByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (inventory.ProductionEvent, error)
	SaveProductionEvent(ctx context.Context, e *inventory.ProductionEvent, options ...core.UpdateOptions) error
	SaveReservation(ctx context.Context, r *inventory.Reservation, options ...core.UpdateOptions) error
	UpdateReservation(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...core.UpdateOptions) error
	GetReservations(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...core.QueryOptions) ([]inventory.Reservation, error)
	GetReservationByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (inventory.Reservation, error)
	GetReservation(ctx context.Context, ID uint64, options ...core.QueryOptions) (inventory.Reservation, error)
}

func newRepo(t *testing.T) (repoT, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	return invrepo.NewPostgresRepo(mock), mock
}

func TestSaveProduct(t *testing.T) {
	p := inventory.Product{Sku: "sku1", Upc: "upc1", Name: "name1"}

	t.Run("update affects a row, no insert", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`UPDATE products`).
			WithArgs(p.Sku, p.Upc, p.Name).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.SaveProduct(context.Background(), p); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("update finds nothing, insert is issued", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`UPDATE products`).
			WithArgs(p.Sku, p.Upc, p.Name).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectExec(`INSERT INTO products`).
			WithArgs(p.Sku, p.Upc, p.Name).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.SaveProduct(context.Background(), p); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("update error propagates without falling through to insert", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`UPDATE products`).
			WithArgs(p.Sku, p.Upc, p.Name).
			WillReturnError(errors.New("boom"))

		if err := repo.SaveProduct(context.Background(), p); err == nil {
			t.Error("expected error, got nil")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestSaveProductInventory(t *testing.T) {
	pi := inventory.ProductInventory{Product: inventory.Product{Sku: "sku1"}, Available: 5}

	t.Run("update succeeds", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`UPDATE product_inventory`).
			WithArgs(pi.Sku, pi.Available).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		if err := repo.SaveProductInventory(context.Background(), pi); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("update finds nothing, insert is issued", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(`UPDATE product_inventory`).
			WithArgs(pi.Sku, pi.Available).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectExec(`INSERT INTO product_inventory`).
			WithArgs(pi.Sku, pi.Available).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		if err := repo.SaveProductInventory(context.Background(), pi); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestGetProduct(t *testing.T) {
	t.Run("hit returns product", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`SELECT sku, upc, name FROM products WHERE sku = \$1`).
			WithArgs("sku1").
			WillReturnRows(pgxmock.NewRows([]string{"sku", "upc", "name"}).
				AddRow("sku1", "upc1", "name1"))

		got, err := repo.GetProduct(context.Background(), "sku1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := inventory.Product{Sku: "sku1", Upc: "upc1", Name: "name1"}
		if got != want {
			t.Errorf("got=%+v want=%+v", got, want)
		}
	})

	t.Run("ErrNoRows maps to ErrNotFound", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`SELECT sku, upc, name FROM products`).
			WithArgs("missing").
			WillReturnError(pgx.ErrNoRows)

		_, err := repo.GetProduct(context.Background(), "missing")
		if !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestGetProductInventory(t *testing.T) {
	t.Run("hit returns inventory", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`SELECT p\.sku, p\.upc, p\.name, pi\.available FROM products p, product_inventory pi`).
			WithArgs("sku1").
			WillReturnRows(pgxmock.NewRows([]string{"sku", "upc", "name", "available"}).
				AddRow("sku1", "upc1", "name1", int64(7)))

		got, err := repo.GetProductInventory(context.Background(), "sku1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Sku != "sku1" || got.Available != 7 {
			t.Errorf("unexpected result: %+v", got)
		}
	})
}

func TestGetAllProductInventory(t *testing.T) {
	repo, mock := newRepo(t)
	mock.ExpectQuery(`SELECT p\.sku, p\.upc, p\.name, pi\.available .* LIMIT \$1 OFFSET \$2`).
		WithArgs(10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"sku", "upc", "name", "available"}).
			AddRow("a", "ua", "na", int64(1)).
			AddRow("b", "ub", "nb", int64(2)))

	got, err := repo.GetAllProductInventory(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Sku != "a" || got[1].Sku != "b" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestSaveProductionEvent(t *testing.T) {
	t.Run("RETURNING id is scanned back into the event", func(t *testing.T) {
		repo, mock := newRepo(t)
		ev := &inventory.ProductionEvent{RequestID: "req1", Sku: "sku1", Quantity: 4, Created: time.Unix(0, 0).UTC()}
		mock.ExpectQuery(`INSERT INTO production_events`).
			WithArgs(ev.RequestID, ev.Sku, ev.Quantity, ev.Created).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uint64(42)))

		if err := repo.SaveProductionEvent(context.Background(), ev); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ev.ID != 42 {
			t.Errorf("expected ID=42 (set by RETURNING), got %d", ev.ID)
		}
	})
}

func TestGetProductionEventByRequestID(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(`SELECT id, request_id, sku, quantity, created FROM production_events`).
		WithArgs("req1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "sku", "quantity", "created"}).
			AddRow(uint64(7), "req1", "sku1", int64(3), created))

	got, err := repo.GetProductionEventByRequestID(context.Background(), "req1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 7 || got.RequestID != "req1" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestSaveReservation(t *testing.T) {
	repo, mock := newRepo(t)
	r := &inventory.Reservation{
		RequestID: "req1", Requester: "x", Sku: "sku1",
		State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 5, Created: time.Unix(0, 0).UTC(),
	}
	mock.ExpectQuery(`INSERT INTO reservations`).
		WithArgs(r.RequestID, r.Requester, r.Sku, r.State, r.ReservedQuantity, r.RequestedQuantity, r.Created).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uint64(99)))

	if err := repo.SaveReservation(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.ID != 99 {
		t.Errorf("expected ID=99, got %d", r.ID)
	}
}

func TestUpdateReservation(t *testing.T) {
	repo, mock := newRepo(t)
	mock.ExpectExec(`UPDATE reservations SET state = \$2, reserved_quantity = \$3 WHERE id=\$1`).
		WithArgs(uint64(7), inventory.Closed, int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.UpdateReservation(context.Background(), 7, inventory.Closed, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetReservations(t *testing.T) {
	t.Run("no filters builds bare LIMIT/OFFSET query", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations  ORDER BY created ASC LIMIT \$1 OFFSET \$2`).
			WithArgs(10, 0).
			WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"}))

		got, err := repo.GetReservations(context.Background(), inventory.GetReservationsOptions{}, 10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 rows, got %d", len(got))
		}
	})

	t.Run("sku filter adds a WHERE clause and arg", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`WHERE  sku = \$3 .* LIMIT \$1 OFFSET \$2`).
			WithArgs(10, 0, "sku1").
			WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"}))

		_, err := repo.GetReservations(context.Background(), inventory.GetReservationsOptions{Sku: "sku1"}, 10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("sku and state filters add two WHERE conditions and args", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(`WHERE  sku = \$3 AND state = \$4 .* LIMIT \$1 OFFSET \$2`).
			WithArgs(10, 0, "sku1", inventory.Open).
			WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"}))

		_, err := repo.GetReservations(context.Background(), inventory.GetReservationsOptions{Sku: "sku1", State: inventory.Open}, 10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestGetReservation(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(`FROM reservations WHERE id = \$1`).
		WithArgs(uint64(7)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"}).
			AddRow(uint64(7), "req1", "x", "sku1", inventory.Open, int64(0), int64(5), created))

	got, err := repo.GetReservation(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestGetReservationByRequestID(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(`FROM reservations WHERE request_id = \$1`).
		WithArgs("req1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"}).
			AddRow(uint64(7), "req1", "x", "sku1", inventory.Open, int64(0), int64(5), created))

	got, err := repo.GetReservationByRequestID(context.Background(), "req1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RequestID != "req1" {
		t.Errorf("unexpected result: %+v", got)
	}
}
