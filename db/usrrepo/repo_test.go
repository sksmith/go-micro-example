package usrrepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
	"github.com/sksmith/go-micro-example/db/usrrepo"
)

func newRepo(t *testing.T) (user.Repository, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	return usrrepo.NewPostgresRepo(mock), mock
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name      string
		user      user.User
		expectSQL string
		execErr   error
		wantErr   bool
	}{
		{
			name:      "insert succeeds",
			user:      user.User{Username: "alice", HashedPassword: "h", IsAdmin: false, Created: time.Unix(0, 0).UTC()},
			expectSQL: `^\s*INSERT INTO users \(username, password, is_admin, created_at\)\s+VALUES \(\$1, \$2, \$3, \$4\);?\s*$`,
		},
		{
			name:      "exec error is propagated",
			user:      user.User{Username: "bob", HashedPassword: "h", IsAdmin: true, Created: time.Unix(0, 0).UTC()},
			expectSQL: `^\s*INSERT INTO users \(username, password, is_admin, created_at\)\s+VALUES \(\$1, \$2, \$3, \$4\);?\s*$`,
			execErr:   errors.New("boom"),
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, mock := newRepo(t)

			expect := mock.ExpectExec(test.expectSQL).
				WithArgs(test.user.Username, test.user.HashedPassword, test.user.IsAdmin, test.user.Created)
			if test.execErr != nil {
				expect.WillReturnError(test.execErr)
			} else {
				expect.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			}

			err := repo.Create(context.Background(), &test.user)
			if test.wantErr && err == nil {
				t.Errorf("expected error, got none")
			}
			if !test.wantErr && err != nil {
				t.Errorf("did not want error, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet expectations: %v", err)
			}
		})
	}
}

func TestGet(t *testing.T) {
	row := user.User{Username: "alice", HashedPassword: "h", IsAdmin: true, Created: time.Unix(0, 0).UTC()}
	const selectUser = `^SELECT username, password, is_admin, created_at FROM users WHERE username = \$1\s*$`

	t.Run("hit returns row", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(selectUser).
			WithArgs(row.Username).
			WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
				AddRow(row.Username, row.HashedPassword, row.IsAdmin, row.Created))

		got, err := repo.Get(context.Background(), row.Username)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != row {
			t.Errorf("got=%+v want=%+v", got, row)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("ErrNoRows maps to ErrNotFound", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectQuery(selectUser).
			WithArgs("missing").
			WillReturnError(pgx.ErrNoRows)

		_, err := repo.Get(context.Background(), "missing")
		if !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("cache hit on second call skips DB", func(t *testing.T) {
		repo, mock := newRepo(t)
		// Only one expectation: the second call must be served from cache.
		mock.ExpectQuery(selectUser).
			WithArgs(row.Username).
			WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
				AddRow(row.Username, row.HashedPassword, row.IsAdmin, row.Created))

		if _, err := repo.Get(context.Background(), row.Username); err != nil {
			t.Fatalf("first get: %v", err)
		}
		if _, err := repo.Get(context.Background(), row.Username); err != nil {
			t.Fatalf("second get: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestDelete(t *testing.T) {
	const deleteUser = `^DELETE FROM users WHERE username = \$1\s*$`

	t.Run("delete succeeds", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(deleteUser).
			WithArgs("alice").
			WillReturnResult(pgxmock.NewResult("DELETE", 1))

		if err := repo.Delete(context.Background(), "alice"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("exec error propagates", func(t *testing.T) {
		repo, mock := newRepo(t)
		mock.ExpectExec(deleteUser).
			WithArgs("alice").
			WillReturnError(errors.New("boom"))

		if err := repo.Delete(context.Background(), "alice"); err == nil {
			t.Error("expected error, got nil")
		}
	})
}
