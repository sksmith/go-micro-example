package db

import (
	"context"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/sksmith/go-micro-example/testutil"
)

type MockConn struct {
	QueryFunc    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRowFunc func(ctx context.Context, sql string, args ...interface{}) pgx.Row
	ExecFunc     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	BeginFunc    func(ctx context.Context) (pgx.Tx, error)
	*testutil.CallWatcher
}

func NewMockConn() MockConn {
	return MockConn{
		QueryFunc:    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) { return nil, nil },
		QueryRowFunc: func(ctx context.Context, sql string, args ...interface{}) pgx.Row { return nil },
		ExecFunc:     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) { return nil, nil },
		BeginFunc:    func(ctx context.Context) (pgx.Tx, error) { return NewMockPgxTx(), nil },
		CallWatcher:  testutil.NewCallWatcher(),
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
	*testutil.CallWatcher
}

func NewMockTransaction() *MockTransaction {
	return &MockTransaction{
		MockConn:     NewMockConn(),
		CommitFunc:   func(ctx context.Context) error { return nil },
		RollbackFunc: func(ctx context.Context) error { return nil },
		CallWatcher:  testutil.NewCallWatcher(),
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

type MockPgxTx struct {
	*testutil.CallWatcher
}

func NewMockPgxTx() *MockPgxTx {
	return &MockPgxTx{
		CallWatcher: testutil.NewCallWatcher(),
	}
}

func (m *MockPgxTx) Begin(ctx context.Context) (pgx.Tx, error) {
	m.AddCall(ctx)
	return nil, nil
}

func (m *MockPgxTx) BeginFunc(ctx context.Context, f func(pgx.Tx) error) (err error) {
	m.AddCall(ctx, f)
	return nil
}

func (m *MockPgxTx) Commit(ctx context.Context) error {
	m.AddCall(ctx)
	return nil
}

func (m *MockPgxTx) Rollback(ctx context.Context) error {
	m.AddCall(ctx)
	return nil
}

func (m *MockPgxTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	m.AddCall(ctx, tableName, columnNames, rowSrc)
	return 0, nil
}

func (m *MockPgxTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	m.AddCall(ctx, b)
	return nil
}

func (m *MockPgxTx) LargeObjects() pgx.LargeObjects {
	m.AddCall()
	return pgx.LargeObjects{}
}

func (m *MockPgxTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	m.AddCall(ctx, name, sql)
	return nil, nil
}

func (m *MockPgxTx) Exec(ctx context.Context, sql string, arguments ...interface{}) (commandTag pgconn.CommandTag, err error) {
	m.AddCall(ctx, sql, arguments)
	return nil, nil
}

func (m *MockPgxTx) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	m.AddCall(ctx, sql, args)
	return nil, nil
}

func (m *MockPgxTx) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	m.AddCall(ctx, sql, args)
	return nil
}

func (m *MockPgxTx) QueryFunc(ctx context.Context, sql string, args []interface{}, scans []interface{}, f func(pgx.QueryFuncRow) error) (pgconn.CommandTag, error) {
	m.AddCall(ctx, sql, args, scans, f)
	return nil, nil
}

func (m *MockPgxTx) Conn() *pgx.Conn {
	m.AddCall()
	return nil
}
