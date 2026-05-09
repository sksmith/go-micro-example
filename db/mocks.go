package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type MockConn struct {
	QueryFunc    func(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRowFunc func(ctx context.Context, sql string, args ...interface{}) pgx.Row
	ExecFunc     func(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	BeginFunc    func(ctx context.Context) (pgx.Tx, error)

	QueryCalls    int
	QueryRowCalls int
	ExecCalls     int
	BeginCalls    int
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
	c.QueryCalls++
	return c.QueryFunc(ctx, sql, args)
}

func (c *MockConn) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	c.QueryRowCalls++
	return c.QueryRowFunc(ctx, sql, args)
}

func (c *MockConn) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	c.ExecCalls++
	return c.ExecFunc(ctx, sql, args)
}

func (c *MockConn) Begin(ctx context.Context) (pgx.Tx, error) {
	c.BeginCalls++
	return c.BeginFunc(ctx)
}

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

type MockPgxTx struct {
	BeginCalls        int
	CommitCalls       int
	RollbackCalls     int
	CopyFromCalls     int
	SendBatchCalls    int
	LargeObjectsCalls int
	PrepareCalls      int
	ExecCalls         int
	QueryCalls        int
	QueryRowCalls     int
	ConnCalls         int
}

func NewMockPgxTx() *MockPgxTx { return &MockPgxTx{} }

func (m *MockPgxTx) Begin(ctx context.Context) (pgx.Tx, error) {
	m.BeginCalls++
	return nil, nil
}

func (m *MockPgxTx) Commit(ctx context.Context) error {
	m.CommitCalls++
	return nil
}

func (m *MockPgxTx) Rollback(ctx context.Context) error {
	m.RollbackCalls++
	return nil
}

func (m *MockPgxTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	m.CopyFromCalls++
	return 0, nil
}

func (m *MockPgxTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	m.SendBatchCalls++
	return nil
}

func (m *MockPgxTx) LargeObjects() pgx.LargeObjects {
	m.LargeObjectsCalls++
	return pgx.LargeObjects{}
}

func (m *MockPgxTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	m.PrepareCalls++
	return nil, nil
}

func (m *MockPgxTx) Exec(ctx context.Context, sql string, arguments ...interface{}) (commandTag pgconn.CommandTag, err error) {
	m.ExecCalls++
	return pgconn.CommandTag{}, nil
}

func (m *MockPgxTx) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	m.QueryCalls++
	return nil, nil
}

func (m *MockPgxTx) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	m.QueryRowCalls++
	return nil
}

func (m *MockPgxTx) Conn() *pgx.Conn {
	m.ConnCalls++
	return nil
}
