package inventory_test

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/sksmith/go-micro-example/internal/inventory"
)

// sqlInjectionPayloads is the SEC-009 spot-check set: classic
// injection patterns, fed in as user-controlled identifiers. The
// goal is to fail loudly if any future change pre-renders the
// payload into the SQL string instead of binding it as a $N
// parameter. pgxmock's anchored regex on the SQL plus its exact
// args matcher together guarantee that.
var sqlInjectionPayloads = []string{
	`'; DROP TABLE products; --`,
	`' OR '1'='1`,
	`sku' UNION SELECT password FROM users --`,
	`\xC0\x27 OR 1=1 --`,
}

// TestGetProductSQLInjectionPayloadsAreBoundParameters: the SKU
// reaches pgx as $1, not as concatenated text.
func TestGetProductSQLInjectionPayloadsAreBoundParameters(t *testing.T) {
	for _, payload := range sqlInjectionPayloads {
		t.Run(payload, func(t *testing.T) {
			repo, mock := newRepo(t)
			mock.ExpectQuery(selectProduct).
				WithArgs(payload).
				WillReturnRows(pgxmock.NewRows([]string{"sku", "upc", "name"}).
					AddRow(payload, "upc", "name"))

			if _, err := repo.GetProduct(context.Background(), payload); err != nil {
				t.Fatalf("GetProduct(%q): %v", payload, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("payload %q reached SQL un-parameterised: %v", payload, err)
			}
		})
	}
}

// TestGetReservationsSQLInjectionPayloadsAreBoundParameters checks
// the dynamic-WHERE builder in GetReservations. That code path
// assembles the SQL at runtime based on which filters are present;
// the placeholder indices ($3, $4) are computed but the user data
// is still appended to params, not interpolated. The pgxmock regex
// for listReservationsBySku expects `sku = \$3` literally — a
// regression that Sprintf'd the payload into the SQL would change
// the rendered string and fail the match.
func TestGetReservationsSQLInjectionPayloadsAreBoundParameters(t *testing.T) {
	for _, payload := range sqlInjectionPayloads {
		t.Run(payload, func(t *testing.T) {
			repo, mock := newRepo(t)
			mock.ExpectQuery(listReservationsBySku).
				WithArgs(10, 0, payload).
				WillReturnRows(pgxmock.NewRows([]string{
					"id", "request_id", "requester", "sku",
					"state", "reserved_quantity", "requested_quantity", "created",
				}))

			_, err := repo.GetReservations(
				context.Background(),
				inventory.GetReservationsOptions{Sku: payload},
				10, 0,
			)
			if err != nil {
				t.Fatalf("GetReservations(%q): %v", payload, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("payload %q reached SQL un-parameterised: %v", payload, err)
			}
		})
	}
}
