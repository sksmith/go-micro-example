package inventory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/internal/inventory"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

// repoT exposes only the methods invrepo's *dbRepo provides; the
// concrete return type from NewPostgresRepo is unexported.
type repoT interface {
	SaveProduct(ctx context.Context, p inventory.Product, options ...persistence.UpdateOptions) error
	SaveProductInventory(ctx context.Context, pi inventory.ProductInventory, options ...persistence.UpdateOptions) error
	GetProduct(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.Product, error)
	GetProductInventory(ctx context.Context, sku string, options ...persistence.QueryOptions) (inventory.ProductInventory, error)
	GetAllProductInventory(ctx context.Context, limit, offset int, options ...persistence.QueryOptions) ([]inventory.ProductInventory, error)
	GetProductionEventByRequestID(ctx context.Context, requestID string, options ...persistence.QueryOptions) (inventory.ProductionEvent, error)
	SaveProductionEvent(ctx context.Context, e *inventory.ProductionEvent, options ...persistence.UpdateOptions) error
	SaveReservation(ctx context.Context, r *inventory.Reservation, options ...persistence.UpdateOptions) error
	UpdateReservation(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...persistence.UpdateOptions) error
	GetReservations(ctx context.Context, resOptions inventory.GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]inventory.Reservation, error)
	GetReservationByRequestID(ctx context.Context, requestID string, options ...persistence.QueryOptions) (inventory.Reservation, error)
	GetReservation(ctx context.Context, ID uint64, options ...persistence.QueryOptions) (inventory.Reservation, error)
	BeginTransaction(ctx context.Context) (persistence.Transaction, error)
}

func newRepo(t *testing.T) (repoT, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	return inventory.NewPostgresRepo(mock), mock
}

// SQL pattern constants, anchored with ^…$ and explicit \s+ for the
// multi-line indented queries in repo.go. The default
// QueryMatcherRegexp is substring-permissive; without anchors a typo
// like "proudcts" would still match.
const (
	updateProduct          = `^\s*UPDATE products\s+SET upc = \$2, name = \$3\s+WHERE sku = \$1;?\s*$`
	insertProduct          = `^\s*INSERT INTO products \(sku, upc, name\)\s+VALUES \(\$1, \$2, \$3\);?\s*$`
	updateProductInventory = `^\s*UPDATE product_inventory\s+SET available = \$2\s+WHERE sku = \$1;?\s*$`
	insertProductInventory = `^INSERT INTO product_inventory \(sku, available\)\s+VALUES \(\$1, \$2\);?\s*$`

	selectProduct          = `^SELECT sku, upc, name FROM products WHERE sku = \$1\s*$`
	selectProductInventory = `^SELECT p\.sku, p\.upc, p\.name, pi\.available FROM products p, product_inventory pi WHERE p\.sku = \$1 AND p\.sku = pi\.sku\s*$`
	selectAllInventory     = `^SELECT p\.sku, p\.upc, p\.name, pi\.available FROM products p, product_inventory pi WHERE p\.sku = pi\.sku ORDER BY p\.sku LIMIT \$1 OFFSET \$2\s*$`

	insertProductionEvent  = `^INSERT INTO production_events \(request_id, sku, quantity, created\)\s+VALUES \(\$1, \$2, \$3, \$4\) RETURNING id;?\s*$`
	selectProductionEvent  = `^SELECT id, request_id, sku, quantity, created FROM production_events\s+WHERE request_id = \$1\s*$`
	insertReservation      = `^INSERT INTO reservations \(request_id, requester, sku, state, reserved_quantity, requested_quantity, created\)\s+VALUES \(\$1, \$2, \$3, \$4, \$5, \$6, \$7\) RETURNING id;?\s*$`
	updateReservationStmt  = `^UPDATE reservations SET state = \$2, reserved_quantity = \$3 WHERE id=\$1;?\s*$`
	selectReservationByID  = `^SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations WHERE id = \$1\s*$`
	selectReservationByReq = `^SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations WHERE request_id = \$1\s*$`

	// Reservation list patterns vary by what filters are present.
	listReservationsBare   = `^SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations\s+ORDER BY created ASC LIMIT \$1 OFFSET \$2\s*$`
	listReservationsBySku  = `^SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations  WHERE  sku = \$3 ORDER BY created ASC LIMIT \$1 OFFSET \$2\s*$`
	listReservationsByBoth = `^SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations  WHERE  sku = \$3 AND state = \$4 ORDER BY created ASC LIMIT \$1 OFFSET \$2\s*$`
)

func TestRepositorySaveProduct(t *testing.T) {
	p := inventory.Product{Sku: "sku1", Upc: "upc1", Name: "name1"}

	t.Run("update affects a row, no insert", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(updateProduct).
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
		mock.ExpectExec(updateProduct).
			WithArgs(p.Sku, p.Upc, p.Name).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectExec(insertProduct).
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
		mock.ExpectExec(updateProduct).
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

func TestRepositorySaveProductInventory(t *testing.T) {
	pi := inventory.ProductInventory{Product: inventory.Product{Sku: "sku1"}, Available: 5}

	t.Run("update succeeds", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(updateProductInventory).
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
		mock.ExpectExec(updateProductInventory).
			WithArgs(pi.Sku, pi.Available).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectExec(insertProductInventory).
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

func TestRepositoryGetProduct(t *testing.T) {
	t.Run("hit returns product", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(selectProduct).
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
		mock.ExpectQuery(selectProduct).
			WithArgs("missing").
			WillReturnError(pgx.ErrNoRows)

		_, err := repo.GetProduct(context.Background(), "missing")
		if !errors.Is(err, persistence.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestRepositoryGetProductInventory(t *testing.T) {
	t.Run("hit returns inventory", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(selectProductInventory).
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

func TestRepositoryGetAllProductInventory(t *testing.T) {
	repo, mock := newRepo(t)
	mock.ExpectQuery(selectAllInventory).
		WithArgs(10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"sku", "upc", "name", "available"}).
			AddRow("a", "ua", "na", int64(1)).
			AddRow("b", "ub", "nb", int64(2))).
		RowsWillBeClosed()

	got, err := repo.GetAllProductInventory(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Sku != "a" || got[1].Sku != "b" {
		t.Errorf("unexpected result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositorySaveProductionEvent(t *testing.T) {
	t.Run("RETURNING id is scanned back into the event", func(t *testing.T) {
		repo, mock := newRepo(t)
		ev := &inventory.ProductionEvent{RequestID: "req1", Sku: "sku1", Quantity: 4, Created: time.Unix(0, 0).UTC()}
		mock.ExpectQuery(insertProductionEvent).
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

func TestRepositoryGetProductionEventByRequestID(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(selectProductionEvent).
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

func TestRepositorySaveReservation(t *testing.T) {
	repo, mock := newRepo(t)
	r := &inventory.Reservation{
		RequestID: "req1", Requester: "x", Sku: "sku1",
		State: inventory.Open, ReservedQuantity: 0, RequestedQuantity: 5, Created: time.Unix(0, 0).UTC(),
	}
	mock.ExpectQuery(insertReservation).
		WithArgs(r.RequestID, r.Requester, r.Sku, r.State, r.ReservedQuantity, r.RequestedQuantity, r.Created).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uint64(99)))

	if err := repo.SaveReservation(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.ID != 99 {
		t.Errorf("expected ID=99, got %d", r.ID)
	}
}

func TestRepositoryUpdateReservation(t *testing.T) {
	repo, mock := newRepo(t)
	mock.ExpectExec(updateReservationStmt).
		WithArgs(uint64(7), inventory.Closed, int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.UpdateReservation(context.Background(), 7, inventory.Closed, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepositoryGetReservations(t *testing.T) {
	emptyRows := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "request_id", "requester", "sku", "state", "reserved_quantity", "requested_quantity", "created"})
	}

	t.Run("no filters builds bare LIMIT/OFFSET query", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(listReservationsBare).
			WithArgs(10, 0).
			WillReturnRows(emptyRows()).
			RowsWillBeClosed()

		got, err := repo.GetReservations(context.Background(), inventory.GetReservationsOptions{}, 10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 rows, got %d", len(got))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("sku filter adds a WHERE clause and arg", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(listReservationsBySku).
			WithArgs(10, 0, "sku1").
			WillReturnRows(emptyRows()).
			RowsWillBeClosed()

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
		mock.ExpectQuery(listReservationsByBoth).
			WithArgs(10, 0, "sku1", inventory.Open).
			WillReturnRows(emptyRows()).
			RowsWillBeClosed()

		_, err := repo.GetReservations(context.Background(), inventory.GetReservationsOptions{Sku: "sku1", State: inventory.Open}, 10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestRepositoryGetReservation(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(selectReservationByID).
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

func TestRepositoryGetReservationByRequestID(t *testing.T) {
	repo, mock := newRepo(t)
	created := time.Unix(0, 0).UTC()
	mock.ExpectQuery(selectReservationByReq).
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

func TestRepositoryBeginTransaction(t *testing.T) {
	t.Run("delegates to conn.Begin", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectBegin()

		tx, err := repo.BeginTransaction(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tx == nil {
			t.Fatal("expected non-nil transaction")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("Begin error propagates", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectBegin().WillReturnError(errors.New("boom"))

		_, err := repo.BeginTransaction(context.Background())
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}
