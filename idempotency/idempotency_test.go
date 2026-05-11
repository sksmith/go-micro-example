package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/idempotency"
)

// fakePool adapts pgxmock's PgxPoolIface to the small idempotency.Pool
// surface.
type fakePool struct{ pgxmock.PgxPoolIface }

func newPool(t *testing.T) fakePool {
	t.Helper()
	p, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	return fakePool{p}
}

func TestApplyRunsHandlerOnFirstDelivery(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	pool.ExpectExec(`INSERT INTO processed_events`).
		WithArgs("event-1", "test-group").
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 1"))

	a := idempotency.NewApplier(pool, "test-group")
	called := 0
	err := a.Apply(context.Background(), "event-1", func(ctx context.Context) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if called != 1 {
		t.Errorf("handler ran %d times, want 1", called)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestApplySkipsHandlerOnRedelivery(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	pool.ExpectExec(`INSERT INTO processed_events`).
		WithArgs("event-1", "test-group").
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 0"))

	a := idempotency.NewApplier(pool, "test-group")
	called := 0
	err := a.Apply(context.Background(), "event-1", func(ctx context.Context) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if called != 0 {
		t.Errorf("handler ran %d times on redelivery, want 0", called)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestApplyRollsBackDedupeRowOnHandlerError(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	pool.ExpectExec(`INSERT INTO processed_events`).
		WithArgs("event-1", "test-group").
		WillReturnResult(pgconn.NewCommandTag("INSERT 0 1"))
	pool.ExpectExec(`DELETE FROM processed_events`).
		WithArgs("event-1", "test-group").
		WillReturnResult(pgconn.NewCommandTag("DELETE 1"))

	a := idempotency.NewApplier(pool, "test-group")
	want := errors.New("downstream failed")
	err := a.Apply(context.Background(), "event-1", func(ctx context.Context) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Errorf("Apply error chain = %v, want chain to include %v", err, want)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestApplyRejectsEmptyEventID(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	a := idempotency.NewApplier(pool, "test-group")
	err := a.Apply(context.Background(), "", func(ctx context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected an error for empty event_id")
	}
}

func TestNewApplierRejectsEmptyGroup(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	defer func() {
		if recover() == nil {
			t.Error("expected NewApplier to panic on empty consumer_group")
		}
	}()
	_ = idempotency.NewApplier(pool, "")
}

func TestCleanupOnceDeletesOldRows(t *testing.T) {
	pool := newPool(t)
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM processed_events`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgconn.NewCommandTag("DELETE 7"))

	a := idempotency.NewApplier(pool, "test-group")
	res, err := a.CleanupOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("CleanupOnce: %v", err)
	}
	if res.Pruned != 7 {
		t.Errorf("Pruned = %d, want 7", res.Pruned)
	}
}
