package db

import (
	"context"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/sksmith/go-micro-example/test"
)

type MockConn struct {
	QueryFunc    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRowFunc func(ctx context.Context, sql string, args ...interface{}) pgx.Row
	ExecFunc     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	BeginFunc    func(ctx context.Context) (pgx.Tx, error)
	*test.CallWatcher
}

func NewMockConn() MockConn {
	return MockConn{
		QueryFunc:    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) { return nil, nil },
		QueryRowFunc: func(ctx context.Context, sql string, args ...interface{}) pgx.Row { return nil },
		ExecFunc:     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) { return nil, nil },
		BeginFunc:    func(ctx context.Context) (pgx.Tx, error) { return nil, nil },
		CallWatcher:  test.NewCallWatcher(),
	}
}

func (c *MockConn) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	c.AddCall(ctx, sql, args)
	return c.QueryFunc(ctx, sql, args)
}

func (c *MockConn) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	c.AddCall(ctx, sql, args)
	return c.QueryRowFunc(ctx, sql, args)
}

func (c *MockConn) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	c.AddCall(ctx, sql, args)
	return c.ExecFunc(ctx, sql, args)
}

func (c *MockConn) Begin(ctx context.Context) (pgx.Tx, error) {
	c.AddCall(ctx)
	return c.BeginFunc(ctx)
}

type MockTransaction struct {
	CommitFunc   func(ctx context.Context) error
	RollbackFunc func(ctx context.Context) error

	MockConn
	*test.CallWatcher
}

func NewMockTransaction() *MockTransaction {
	return &MockTransaction{
		MockConn:     NewMockConn(),
		CommitFunc:   func(ctx context.Context) error { return nil },
		RollbackFunc: func(ctx context.Context) error { return nil },
		CallWatcher:  test.NewCallWatcher(),
	}
}

func (t *MockTransaction) Commit(ctx context.Context) error {
	t.AddCall(ctx)
	return t.CommitFunc(ctx)
}

func (t *MockTransaction) Rollback(ctx context.Context) error {
	t.AddCall(ctx)
	return t.RollbackFunc(ctx)
}
