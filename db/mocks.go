package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MockConn satisfies core.Conn. It exists primarily as the embed for
// MockTransaction; service-layer tests rarely interact with it
// directly. Each method delegates to a per-method override hook
// (XxxFunc) so a test can change behavior without subclassing.
//
// Call counters are deliberately *not* tracked here — db.MockConn is
// not a SQL-asserting double. For tests that need to verify what SQL
// hits the database, use `pgxmock` directly against the repo (see
// db/invrepo/repo_test.go and db/usrrepo/repo_test.go).
type MockConn struct {
	QueryFunc    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRowFunc func(ctx context.Context, sql string, args ...interface{}) pgx.Row
	ExecFunc     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	BeginFunc    func(ctx context.Context) (pgx.Tx, error)
}

func NewMockConn() MockConn {
	return MockConn{
		QueryFunc:    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) { return nil, nil },
		QueryRowFunc: func(ctx context.Context, sql string, args ...interface{}) pgx.Row { return nil },
		ExecFunc: func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		BeginFunc: func(ctx context.Context) (pgx.Tx, error) { return NewMockPgxTx(), nil },
	}
}

func (c *MockConn) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return c.QueryFunc(ctx, sql, args)
}

func (c *MockConn) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return c.QueryRowFunc(ctx, sql, args)
}

func (c *MockConn) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return c.ExecFunc(ctx, sql, args)
}

func (c *MockConn) Begin(ctx context.Context) (pgx.Tx, error) {
	return c.BeginFunc(ctx)
}

// MockTransaction satisfies core.Transaction (which is core.Conn +
// Commit/Rollback). Only Commit/Rollback counters are tracked, since
// those are what service-layer tests verify; for SQL-level
// assertions, use pgxmock at the repo layer.
type MockTransaction struct {
	CommitFunc   func(ctx context.Context) error
	RollbackFunc func(ctx context.Context) error

	MockConn

	CommitCalls   int
	RollbackCalls int
}

func NewMockTransaction() *MockTransaction {
	return &MockTransaction{
		MockConn:     NewMockConn(),
		CommitFunc:   func(ctx context.Context) error { return nil },
		RollbackFunc: func(ctx context.Context) error { return nil },
	}
}

func (t *MockTransaction) Commit(ctx context.Context) error {
	t.CommitCalls++
	return t.CommitFunc(ctx)
}

func (t *MockTransaction) Rollback(ctx context.Context) error {
	t.RollbackCalls++
	return t.RollbackFunc(ctx)
}

// MockPgxTx satisfies pgx.Tx so it can be returned from
// MockConn.Begin. Service tests assert on CommitCalls / RollbackCalls
// for sub-transactions; the rest of pgx.Tx is implemented as no-op
// stubs solely to satisfy the interface.
type MockPgxTx struct {
	CommitCalls   int
	RollbackCalls int
}

func NewMockPgxTx() *MockPgxTx { return &MockPgxTx{} }

func (m *MockPgxTx) Begin(ctx context.Context) (pgx.Tx, error) { return nil, nil }

func (m *MockPgxTx) Commit(ctx context.Context) error {
	m.CommitCalls++
	return nil
}

func (m *MockPgxTx) Rollback(ctx context.Context) error {
	m.RollbackCalls++
	return nil
}

func (m *MockPgxTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (m *MockPgxTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (m *MockPgxTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (m *MockPgxTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (m *MockPgxTx) Exec(ctx context.Context, sql string, arguments ...interface{}) (commandTag pgconn.CommandTag, err error) {
	return pgconn.CommandTag{}, nil
}

func (m *MockPgxTx) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (m *MockPgxTx) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return nil
}

func (m *MockPgxTx) Conn() *pgx.Conn { return nil }
