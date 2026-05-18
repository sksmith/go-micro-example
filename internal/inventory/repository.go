package inventory

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

type dbRepo struct {
	conn persistence.Conn
}

func NewPostgresRepo(conn persistence.Conn) *dbRepo {
	log.Info().Msg("creating inventory repository...")
	return &dbRepo{
		conn: conn,
	}
}

func (d *dbRepo) SaveProduct(ctx context.Context, product Product, options ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("SaveProduct")
	tx := persistence.GetUpdateOptions(d.conn, options...)

	ct, err := tx.Exec(ctx, `
		UPDATE products
           SET upc = $2, name = $3
         WHERE sku = $1;`,
		product.Sku, product.Upc, product.Name)
	if err != nil {
		m.Complete(nil)
		return err
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

func (d *dbRepo) SaveProductInventory(ctx context.Context, productInventory ProductInventory, options ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("SaveProductInventory")
	tx := persistence.GetUpdateOptions(d.conn, options...)

	ct, err := tx.Exec(ctx, `
		UPDATE product_inventory
           SET available = $2
         WHERE sku = $1;`,
		productInventory.Sku, productInventory.Available)
	if err != nil {
		m.Complete(nil)
		return err
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

func (d *dbRepo) GetProduct(ctx context.Context, sku string, options ...persistence.QueryOptions) (Product, error) {
	m := persistence.StartMetric("GetProduct")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	product := Product{}
	err := tx.QueryRow(ctx, `SELECT sku, upc, name FROM products WHERE sku = $1 `+forUpdate, sku).
		Scan(&product.Sku, &product.Upc, &product.Name)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return product, persistence.ErrNotFound
		}
		return product, err
	}

	m.Complete(nil)
	return product, nil
}

func (d *dbRepo) GetProductInventory(ctx context.Context, sku string, options ...persistence.QueryOptions) (ProductInventory, error) {
	m := persistence.StartMetric("GetProductInventory")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	productInventory := ProductInventory{}
	err := tx.QueryRow(ctx, `SELECT p.sku, p.upc, p.name, pi.available FROM products p, product_inventory pi WHERE p.sku = $1 AND p.sku = pi.sku `+forUpdate, sku).
		Scan(&productInventory.Sku, &productInventory.Upc, &productInventory.Name, &productInventory.Available)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return productInventory, persistence.ErrNotFound
		}
		return productInventory, err
	}

	m.Complete(nil)
	return productInventory, nil
}

func (d *dbRepo) GetAllProductInventory(ctx context.Context, limit int, offset int, options ...persistence.QueryOptions) ([]ProductInventory, error) {
	m := persistence.StartMetric("GetAllProducts")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	products := make([]ProductInventory, 0)
	rows, err := tx.Query(ctx,
		`SELECT p.sku, p.upc, p.name, pi.available FROM products p, product_inventory pi WHERE p.sku = pi.sku ORDER BY p.sku LIMIT $1 OFFSET $2 `+forUpdate,
		limit, offset)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return products, persistence.ErrNotFound
		}
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		product := ProductInventory{}
		err = rows.Scan(&product.Sku, &product.Upc, &product.Name, &product.Available)
		if err != nil {
			m.Complete(err)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, persistence.ErrNotFound
			}
			return nil, err
		}
		products = append(products, product)
	}

	m.Complete(nil)
	return products, nil
}

func (d *dbRepo) GetProductionEventByRequestID(ctx context.Context, requestID string, options ...persistence.QueryOptions) (pe ProductionEvent, err error) {
	m := persistence.StartMetric("GetProductionEventByRequestID")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	pe = ProductionEvent{}
	err = tx.QueryRow(ctx, `SELECT id, request_id, sku, quantity, created FROM production_events `+forUpdate+` WHERE request_id = $1 `+forUpdate, requestID).
		Scan(&pe.ID, &pe.RequestID, &pe.Sku, &pe.Quantity, &pe.Created)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return pe, persistence.ErrNotFound
		}
		return pe, err
	}

	m.Complete(nil)
	return pe, nil
}

func (d *dbRepo) SaveProductionEvent(ctx context.Context, event *ProductionEvent, options ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("SaveProductionEvent")
	tx := persistence.GetUpdateOptions(d.conn, options...)

	insert := `INSERT INTO production_events (request_id, sku, quantity, created)
			       VALUES ($1, $2, $3, $4) RETURNING id;`

	err := tx.QueryRow(ctx, insert, event.RequestID, event.Sku, event.Quantity, event.Created).Scan(&event.ID)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ErrNotFound
		}
		return err
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) SaveReservation(ctx context.Context, r *Reservation, options ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("SaveReservation")
	tx := persistence.GetUpdateOptions(d.conn, options...)

	insert := `INSERT INTO reservations (request_id, requester, sku, state, reserved_quantity, requested_quantity, created)
                      VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;`
	err := tx.QueryRow(ctx, insert, r.RequestID, r.Requester, r.Sku, r.State, r.ReservedQuantity, r.RequestedQuantity, r.Created).Scan(&r.ID)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ErrNotFound
		}
		return err
	}
	m.Complete(nil)
	return nil
}

func (d *dbRepo) UpdateReservation(ctx context.Context, ID uint64, state ReserveState, qty int64, options ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("UpdateReservation")
	tx := persistence.GetUpdateOptions(d.conn, options...)

	update := `UPDATE reservations SET state = $2, reserved_quantity = $3 WHERE id=$1;`
	_, err := tx.Exec(ctx, update, ID, state, qty)
	m.Complete(err)
	if err != nil {
		return err
	}
	m.Complete(nil)
	return nil
}

const reservationFields = "id, request_id, requester, sku, state, reserved_quantity, requested_quantity, created"

func (d *dbRepo) GetReservations(ctx context.Context, resOptions GetReservationsOptions, limit, offset int, options ...persistence.QueryOptions) ([]Reservation, error) {
	m := persistence.StartMetric("GetSkuOpenReserves")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	params := make([]interface{}, 0)
	params = append(params, limit)
	params = append(params, offset)

	whereClause := ""
	paramIdx := 2

	if resOptions.Sku != "" || resOptions.State != None {
		whereClause = " WHERE "
	}

	if resOptions.Sku != "" {
		if paramIdx > 2 {
			whereClause += " AND"
		}
		paramIdx++
		whereClause += " sku = $" + strconv.Itoa(paramIdx)
		params = append(params, resOptions.Sku)
	}

	if resOptions.State != None {
		if paramIdx > 2 {
			whereClause += " AND"
		}
		paramIdx++
		whereClause += " state = $" + strconv.Itoa(paramIdx)
		params = append(params, resOptions.State)
	}

	reservations := make([]Reservation, 0)
	rows, err := tx.Query(ctx,
		`SELECT `+reservationFields+` FROM reservations `+whereClause+` ORDER BY created ASC LIMIT $1 OFFSET $2 `+forUpdate,
		params...)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return reservations, persistence.ErrNotFound
		}
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		r := Reservation{}
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

func (d *dbRepo) GetReservationByRequestID(ctx context.Context, requestId string, options ...persistence.QueryOptions) (Reservation, error) {
	m := persistence.StartMetric("GetReservationByRequestID")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	r := Reservation{}
	err := tx.QueryRow(ctx,
		`SELECT `+reservationFields+` FROM reservations WHERE request_id = $1 `+forUpdate,
		requestId).Scan(&r.ID, &r.RequestID, &r.Requester, &r.Sku, &r.State, &r.ReservedQuantity, &r.RequestedQuantity, &r.Created)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return r, persistence.ErrNotFound
		}
		return r, err
	}

	m.Complete(nil)
	return r, nil
}

func (d *dbRepo) GetReservation(ctx context.Context, ID uint64, options ...persistence.QueryOptions) (Reservation, error) {
	m := persistence.StartMetric("GetReservation")
	tx, forUpdate := persistence.GetQueryOptions(d.conn, options...)

	r := Reservation{}
	err := tx.QueryRow(ctx,
		`SELECT `+reservationFields+` FROM reservations WHERE id = $1 `+forUpdate, ID).
		Scan(&r.ID, &r.RequestID, &r.Requester, &r.Sku, &r.State, &r.ReservedQuantity, &r.RequestedQuantity, &r.Created)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return r, persistence.ErrNotFound
		}
		return r, err
	}

	m.Complete(nil)
	return r, nil
}

func (d *dbRepo) BeginTransaction(ctx context.Context) (persistence.Transaction, error) {
	tx, err := d.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}
