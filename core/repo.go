package core

import (
	"context"
	"errors"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
)

var ErrNotFound = errors.New("core: record not found")

type Transaction interface {
	Conn
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type Conn interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

type UpdateOptions struct {
	Tx Transaction
}

type QueryOptions struct {
	ForUpdate bool
	Tx        Transaction
}
