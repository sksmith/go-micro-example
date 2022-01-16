package invrepo

import (
	"context"

	"github.com/jackc/pgx/v4"
	"github.com/pkg/errors"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/sksmith/go-micro-example/db"
)

type dbRepo struct {
	conn core.Conn
}

func NewPostgresRepo(conn core.Conn) inventory.Repository {
	return &dbRepo{
		conn: conn,
	}
}

func (d *dbRepo) SaveProduct(ctx context.Context, product inventory.Product, options ...core.UpdateOptions) error {
	m := db.StartMetric("SaveProduct")
	tx := db.GetUpdateOptions(d.conn, options...)

	ct, err := tx.Exec(ctx, `
		UPDATE products
           SET upc = $2, name = $3
         WHERE sku = $1;`,
		product.Sku, product.Upc, product.Name)
	if err != nil {
		m.Complete(nil)
		return errors.WithStack(err)
	}
	if ct.RowsAffected() == 0 {
		_, err := tx.Exec(ctx, `
		INSERT INTO products (sku, upc, name)
                      VALUES ($1, $2, $3);`,
			product.Sku, product.Upc, product.Name)
		if err != nil {
			m.Complete(err)
			return err
		}
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) SaveProductInventory(ctx context.Context, productInventory inventory.ProductInventory, options ...core.UpdateOptions) error {
	m := db.StartMetric("SaveProductInventory")
	tx := db.GetUpdateOptions(d.conn, options...)

	ct, err := tx.Exec(ctx, `
		UPDATE product_inventory
           SET available = $2
         WHERE sku = $1;`,
		productInventory.Sku, productInventory.Available)
	if err != nil {
		m.Complete(nil)
		return errors.WithStack(err)
	}
	if ct.RowsAffected() == 0 {
		insert := `INSERT INTO product_inventory (sku, available)
                      VALUES ($1, $2);`
		_, err := tx.Exec(ctx, insert, productInventory.Sku, productInventory.Available)
		m.Complete(err)
		if err != nil {
			return err
		}
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) GetProduct(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.Product, error) {
	m := db.StartMetric("GetProduct")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	product := inventory.Product{}
	err := tx.QueryRow(ctx, `SELECT sku, upc, name FROM products WHERE sku = $1 `+forUpdate, sku).
		Scan(&product.Sku, &product.Upc, &product.Name)

	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return product, errors.WithStack(core.ErrNotFound)
		}
		return product, errors.WithStack(err)
	}

	m.Complete(nil)
	return product, nil
}

func (d *dbRepo) GetProductInventory(ctx context.Context, sku string, options ...core.QueryOptions) (inventory.ProductInventory, error) {
	m := db.StartMetric("GetProductInventory")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	productInventory := inventory.ProductInventory{}
	err := tx.QueryRow(ctx, `SELECT p.sku, p.upc, p.name, pi.available FROM products p, product_inventory pi WHERE p.sku = $1 AND p.sku = pi.sku `+forUpdate, sku).
		Scan(&productInventory.Sku, &productInventory.Upc, &productInventory.Name, &productInventory.Available)

	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return productInventory, errors.WithStack(core.ErrNotFound)
		}
		return productInventory, errors.WithStack(err)
	}

	m.Complete(nil)
	return productInventory, nil
}

func (d *dbRepo) GetAllProductInventory(ctx context.Context, limit int, offset int, options ...core.QueryOptions) ([]inventory.ProductInventory, error) {
	m := db.StartMetric("GetAllProducts")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	products := make([]inventory.ProductInventory, 0)
	rows, err := tx.Query(ctx,
		`SELECT p.sku, p.upc, p.name, pi.available FROM products p, product_inventory pi WHERE p.sku = pi.sku ORDER BY p.sku LIMIT $1 OFFSET $2 `+forUpdate,
		limit, offset)
	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return products, errors.WithStack(core.ErrNotFound)
		}
		return nil, errors.WithStack(err)
	}
	defer rows.Close()

	for rows.Next() {
		product := inventory.ProductInventory{}
		err = rows.Scan(&product.Sku, &product.Upc, &product.Name, &product.Available)
		if err != nil {
			m.Complete(err)
			if err == pgx.ErrNoRows {
				return nil, errors.WithStack(core.ErrNotFound)
			}
			return nil, errors.WithStack(err)
		}
		products = append(products, product)
	}

	m.Complete(nil)
	return products, nil
}

func (d *dbRepo) GetProductionEventByRequestID(ctx context.Context, requestID string, options ...core.QueryOptions) (pe inventory.ProductionEvent, err error) {
	m := db.StartMetric("GetProductionEventByRequestID")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	pe = inventory.ProductionEvent{}
	err = tx.QueryRow(ctx, `SELECT id, request_id, sku, quantity, created FROM production_events `+forUpdate+` WHERE request_id = $1 `+forUpdate, requestID).
		Scan(&pe.ID, &pe.RequestID, &pe.Sku, &pe.Quantity, &pe.Created)

	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return pe, errors.WithStack(core.ErrNotFound)
		}
		return pe, errors.WithStack(err)
	}

	m.Complete(nil)
	return pe, nil
}

func (d *dbRepo) SaveProductionEvent(ctx context.Context, event *inventory.ProductionEvent, options ...core.UpdateOptions) error {
	m := db.StartMetric("SaveProductionEvent")
	tx := db.GetUpdateOptions(d.conn, options...)

	insert := `INSERT INTO production_events (request_id, sku, quantity, created)
			       VALUES ($1, $2, $3, $4) RETURNING id;`

	err := tx.QueryRow(ctx, insert, event.RequestID, event.Sku, event.Quantity, event.Created).Scan(&event.ID)
	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return errors.WithStack(core.ErrNotFound)
		}
		return errors.WithStack(err)
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) SaveReservation(ctx context.Context, r *inventory.Reservation, options ...core.UpdateOptions) error {
	m := db.StartMetric("SaveReservation")
	tx := db.GetUpdateOptions(d.conn, options...)

	insert := `INSERT INTO reservations (request_id, requester, sku, state, reserved_quantity, requested_quantity, created)
                      VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;`
	err := tx.QueryRow(ctx, insert, r.RequestID, r.Requester, r.Sku, r.State, r.ReservedQuantity, r.RequestedQuantity, r.Created).Scan(&r.ID)
	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return errors.WithStack(core.ErrNotFound)
		}
		return errors.WithStack(err)
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) UpdateReservation(ctx context.Context, ID uint64, state inventory.ReserveState, qty int64, options ...core.UpdateOptions) error {
	m := db.StartMetric("UpdateReservation")
	tx := db.GetUpdateOptions(d.conn, options...)

	update := `UPDATE reservations SET state = $2, reserved_quantity = $3 WHERE id=$1;`
	_, err := tx.Exec(ctx, update, ID, state, qty)
	m.Complete(err)
	if err != nil {
		return errors.WithStack(err)
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) GetSkuReservationsByState(ctx context.Context, sku string, state inventory.ReserveState, limit, offset int, options ...core.QueryOptions) ([]inventory.Reservation, error) {
	m := db.StartMetric("GetSkuOpenReserves")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	params := make([]interface{}, 0)
	params = append(params, sku)
	params = append(params, limit)
	params = append(params, offset)

	whereClause := " WHERE sku = $1"

	if state != inventory.None {
		whereClause += " AND state = $4"
		params = append(params, state)
	}

	reservations := make([]inventory.Reservation, 0)
	rows, err := tx.Query(ctx,
		`SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations `+whereClause+` ORDER BY created ASC LIMIT $2 OFFSET $3 `+forUpdate,
		params...)
	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return reservations, errors.WithStack(core.ErrNotFound)
		}
		return nil, errors.WithStack(err)
	}
	defer rows.Close()

	for rows.Next() {
		r := inventory.Reservation{}
		err = rows.Scan(&r.ID, &r.RequestID, &r.Requester, &r.Sku, &r.State, &r.ReservedQuantity, &r.RequestedQuantity, &r.Created)
		if err != nil {
			m.Complete(err)
			return nil, err
		}
		reservations = append(reservations, r)
	}

	m.Complete(nil)
	return reservations, nil
}

func (d *dbRepo) GetReservationByRequestID(ctx context.Context, requestId string, options ...core.QueryOptions) (inventory.Reservation, error) {
	m := db.StartMetric("GetReservationByRequestID")
	tx, forUpdate := db.GetQueryOptions(d.conn, options...)

	r := inventory.Reservation{}
	err := tx.QueryRow(ctx,
		`SELECT id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created FROM reservations WHERE request_id = $1 `+forUpdate,
		requestId).Scan(&r.ID, &r.RequestID, &r.Requester, &r.Sku, &r.State, &r.ReservedQuantity, &r.RequestedQuantity, &r.Created)
	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return r, errors.WithStack(core.ErrNotFound)
		}
		return r, errors.WithStack(err)
	}

	m.Complete(nil)
	return r, nil
}

func (d *dbRepo) BeginTransaction(ctx context.Context) (core.Transaction, error) {
	tx, err := d.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}
