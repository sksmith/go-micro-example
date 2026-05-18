package user_test

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/internal/user"
)

// sqlInjectionPayloads is the set of strings exercised against the
// repository to confirm that user-supplied content always reaches
// pgx as a bound parameter rather than being interpolated into the
// SQL string. Each payload mimics a classic attack technique; if any
// of them ended up in the rendered SQL the pgxmock anchored-regex
// pattern would fail to match and the test would fail loudly.
var sqlInjectionPayloads = []string{
	`'; DROP TABLE users; --`,
	`' OR '1'='1`,
	`admin' --`,
	`\xC0\x27 OR 1=1 --`,
	`")` + "; DROP TABLE users; --",
}

// TestGetSQLInjectionPayloadsAreBoundParameters confirms that a
// hostile username is forwarded to pgx as the $1 parameter and the
// rendered SQL still contains only the placeholder. The pgxmock
// regex is anchored on the literal placeholder; if a regression
// switched the repo to Sprintf the payload into the SQL, the
// regex would not match and ExpectationsWereMet would fail.
//
// This is the SEC-009 spot-check: parameterisation is enforced by
// pgx's protocol-level prepared statements, but the test gates
// against a future change that bypasses that protection (e.g. by
// pre-rendering the SQL).
func TestGetSQLInjectionPayloadsAreBoundParameters(t *testing.T) {
	const selectUser = `^SELECT username, password, is_admin, created_at FROM users WHERE username = \$1\s*$`

	for _, payload := range sqlInjectionPayloads {
		t.Run(payload, func(t *testing.T) {
			repo, mock := newRepo(t)
			// The args matcher rejects anything but the exact payload.
			// The SQL pattern rejects anything but the parameterised
			// statement. Both must hold for the call to succeed.
			mock.ExpectQuery(selectUser).
				WithArgs(payload).
				WillReturnRows(pgxmock.NewRows([]string{"username", "password", "is_admin", "created_at"}).
					AddRow(payload, "h", false, zeroTime()))

			_, err := repo.Get(context.Background(), payload)
			if err != nil {
				t.Fatalf("Get(%q): %v", payload, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("payload %q reached SQL un-parameterised: %v", payload, err)
			}
		})
	}
}

// TestDeleteSQLInjectionPayloadsAreBoundParameters covers the DELETE
// path; the same parameterisation invariant must hold there.
func TestDeleteSQLInjectionPayloadsAreBoundParameters(t *testing.T) {
	const deleteUser = `^DELETE FROM users WHERE username = \$1\s*$`

	for _, payload := range sqlInjectionPayloads {
		t.Run(payload, func(t *testing.T) {
			repo, mock := newRepo(t)
			mock.ExpectExec(deleteUser).
				WithArgs(payload).
				WillReturnResult(pgxmock.NewResult("DELETE", 1))

			if err := repo.Delete(context.Background(), payload); err != nil {
				t.Fatalf("Delete(%q): %v", payload, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("payload %q reached SQL un-parameterised: %v", payload, err)
			}
		})
	}
}

func zeroTime() any { return user.User{}.Created }
