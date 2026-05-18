package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/internal/platform/cache"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
	"github.com/sksmith/go-micro-example/internal/user"
)

// repoForTest holds the repo plus the cache instance the test
// installed (or nil for the no-cache case), so assertions can poke
// at cache state directly.
type repoForTest struct {
	repo interface {
		user.Repository
		SetCache(c cache.Cache, ttl time.Duration)
	}
	mock  pgxmock.PgxConnIface
	cache *cache.MemoryCache
}

func newRepo(t *testing.T) (user.Repository, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	return user.NewPostgresRepo(mock), mock
}

// newRepoWithCache wires a MemoryCache so tests exercise the
// DSN-021c read-through path without depending on Redis.
func newRepoWithCache(t *testing.T) repoForTest {
	t.Helper()
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	c := cache.NewMemoryCache()
	r := user.NewPostgresRepo(mock)
	r.SetCache(c, time.Minute)
	return repoForTest{repo: r, mock: mock, cache: c}
}

func TestRepositoryCreate(t *testing.T) {
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

func TestRepositoryGet(t *testing.T) {
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
		if !errors.Is(err, persistence.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("cache hit on second call skips DB", func(t *testing.T) {
		rt := newRepoWithCache(t)
		// Only one expectation: the second call must be served from cache.
		rt.mock.ExpectQuery(selectUser).
			WithArgs(row.Username).
			WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
				AddRow(row.Username, row.HashedPassword, row.IsAdmin, row.Created))

		if _, err := rt.repo.Get(context.Background(), row.Username); err != nil {
			t.Fatalf("first get: %v", err)
		}
		if _, err := rt.repo.Get(context.Background(), row.Username); err != nil {
			t.Fatalf("second get: %v", err)
		}
		if err := rt.mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("no cache wired: every Get hits DB", func(t *testing.T) {
		repo, mock := newRepo(t)
		// Two expectations: both Gets must reach the DB.
		for i := 0; i < 2; i++ {
			mock.ExpectQuery(selectUser).
				WithArgs(row.Username).
				WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
					AddRow(row.Username, row.HashedPassword, row.IsAdmin, row.Created))
		}
		for i := 0; i < 2; i++ {
			if _, err := repo.Get(context.Background(), row.Username); err != nil {
				t.Fatalf("get %d: %v", i, err)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestCachePopulatedOnCreate(t *testing.T) {
	rt := newRepoWithCache(t)
	u := user.User{Username: "alice", HashedPassword: "h", IsAdmin: false, Created: time.Unix(0, 0).UTC()}
	rt.mock.ExpectExec(`^\s*INSERT INTO users`).
		WithArgs(u.Username, u.HashedPassword, u.IsAdmin, u.Created).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := rt.repo.Create(context.Background(), &u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A follow-up Get must NOT hit the DB; no SELECT expectation is
	// registered, so any query reaching pgxmock would fail the test.
	got, err := rt.repo.Get(context.Background(), u.Username)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got != u {
		t.Errorf("Get after Create = %+v, want %+v", got, u)
	}
	if err := rt.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCacheInvalidatedOnDelete(t *testing.T) {
	rt := newRepoWithCache(t)
	u := user.User{Username: "alice", HashedPassword: "h", IsAdmin: false, Created: time.Unix(0, 0).UTC()}

	// First Get populates the cache.
	rt.mock.ExpectQuery(`^SELECT username, password, is_admin, created_at FROM users WHERE username = \$1\s*$`).
		WithArgs(u.Username).
		WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
			AddRow(u.Username, u.HashedPassword, u.IsAdmin, u.Created))
	if _, err := rt.repo.Get(context.Background(), u.Username); err != nil {
		t.Fatalf("seed Get: %v", err)
	}
	if rt.cache.Size() != 1 {
		t.Fatalf("cache size after Get = %d, want 1", rt.cache.Size())
	}

	// Delete must invalidate the cached entry.
	rt.mock.ExpectExec(`^DELETE FROM users WHERE username = \$1$`).
		WithArgs(u.Username).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := rt.repo.Delete(context.Background(), u.Username); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rt.cache.Size() != 0 {
		t.Errorf("cache size after Delete = %d, want 0", rt.cache.Size())
	}
	if err := rt.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCacheTTLExpiryFallsThroughToDB(t *testing.T) {
	mock, err := pgxmock.NewConn()
	if err != nil {
		t.Fatalf("pgxmock.NewConn: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })
	c := cache.NewMemoryCache()
	r := user.NewPostgresRepo(mock)
	// 10ms TTL so we don't need to sleep long to expire it.
	r.SetCache(c, 10*time.Millisecond)

	row := user.User{Username: "alice", HashedPassword: "h", IsAdmin: false, Created: time.Unix(0, 0).UTC()}
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`^SELECT username`).
			WithArgs(row.Username).
			WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
				AddRow(row.Username, row.HashedPassword, row.IsAdmin, row.Created))
	}

	if _, err := r.Get(context.Background(), row.Username); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := r.Get(context.Background(), row.Username); err != nil {
		t.Fatalf("second Get (after TTL): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDelete(t *testing.T) {
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
