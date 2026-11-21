package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"
)

const orderAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// OrderSlip is one request from the website.
type OrderSlip struct {
	Reference      string    `json:"reference"`
	Name           string    `json:"name"`
	Phone          string    `json:"phone"`
	WorkerID       string    `json:"workerId"`
	CatalogPhoneID string    `json:"catalogPhoneId"`
	PhoneModel     string    `json:"phoneModel"`
	IMEI           string    `json:"imei"`
	Visit          string    `json:"visit"`
	Status         string    `json:"status"`
	MithrilAccount *bool     `json:"mithrilAccount"`
	WorkerChannel  string    `json:"workerChannel"`
	SupportChannel string    `json:"supportChannel"`
	CreatedAt      time.Time `json:"createdAt"`
}

// InsertOrder saves a slip, holds one available IMEI, and returns it with a P2O reference.
func (s *Store) InsertOrder(ctx context.Context, order OrderSlip) (OrderSlip, error) {
	var last error
	for range 3 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return OrderSlip{}, err
		}
		// Lock the catalog parent before any unit, matching SaveCatalog.
		// This prevents catalog edits and concurrent reservations deadlocking.
		var lockedID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_phones WHERE id = $1 FOR UPDATE`, order.CatalogPhoneID).Scan(&lockedID)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return OrderSlip{}, ErrOutOfStock
		}
		if err != nil {
			tx.Rollback()
			return OrderSlip{}, err
		}
		order.Reference = newReference()
		order.Status = "open"
		err = tx.QueryRowContext(ctx, `
UPDATE catalog_phone_units SET status = 'reserved', order_reference = $2
WHERE id = (
    SELECT id FROM catalog_phone_units
    WHERE catalog_phone_id = $1 AND status = 'available'
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING imei`, order.CatalogPhoneID, order.Reference).Scan(&order.IMEI)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return OrderSlip{}, ErrOutOfStock
		}
		if err != nil {
			tx.Rollback()
			return OrderSlip{}, err
		}
		err = tx.QueryRowContext(ctx, `
INSERT INTO order_slips (
    reference, customer_name, customer_phone, worker_id, catalog_phone_id, phone_model, imei, visit_window, mithril_account, status
) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, 'open')
RETURNING created_at`,
			order.Reference, order.Name, order.Phone, order.WorkerID, order.CatalogPhoneID, order.PhoneModel, order.IMEI, order.Visit, order.MithrilAccount,
		).Scan(&order.CreatedAt)
		if err != nil {
			tx.Rollback()
			last = err
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE catalog_phones SET in_stock = EXISTS (
    SELECT 1 FROM catalog_phone_units WHERE catalog_phone_id = $1 AND status = 'available'
), updated_at = now() WHERE id = $1`, order.CatalogPhoneID); err != nil {
			tx.Rollback()
			return OrderSlip{}, err
		}
		if err := tx.Commit(); err != nil {
			return OrderSlip{}, err
		}
		return order, nil
	}
	return OrderSlip{}, last
}

// CancelOrder returns the held IMEI to stock.
func (s *Store) CancelOrder(ctx context.Context, reference string) (OrderSlip, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrderSlip{}, err
	}
	defer tx.Rollback()
	var order OrderSlip
	var account sql.NullBool
	err = tx.QueryRowContext(ctx, `
SELECT reference, customer_name, customer_phone, COALESCE(worker_id, ''), COALESCE(catalog_phone_id, ''),
       phone_model, COALESCE(imei, ''), visit_window, status, mithril_account,
       COALESCE(worker_channel, ''), COALESCE(support_channel, ''), created_at
FROM order_slips WHERE reference = $1 FOR UPDATE`, reference).Scan(
		&order.Reference, &order.Name, &order.Phone, &order.WorkerID, &order.CatalogPhoneID,
		&order.PhoneModel, &order.IMEI, &order.Visit, &order.Status, &account,
		&order.WorkerChannel, &order.SupportChannel, &order.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OrderSlip{}, ErrNotFound
	}
	if err != nil {
		return OrderSlip{}, err
	}
	if account.Valid {
		value := account.Bool
		order.MithrilAccount = &value
	}
	if order.Status == "cancelled" {
		return order, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE order_slips SET status = 'cancelled' WHERE reference = $1`, reference); err != nil {
		return OrderSlip{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE catalog_phone_units SET status = 'available', order_reference = NULL
WHERE order_reference = $1 AND status = 'reserved'`, reference); err != nil {
		return OrderSlip{}, err
	}
	if order.CatalogPhoneID != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE catalog_phones SET in_stock = EXISTS (
    SELECT 1 FROM catalog_phone_units WHERE catalog_phone_id = $1 AND status = 'available'
), updated_at = now() WHERE id = $1`, order.CatalogPhoneID); err != nil {
			return OrderSlip{}, err
		}
	}
	order.Status = "cancelled"
	if err := tx.Commit(); err != nil {
		return OrderSlip{}, err
	}
	return order, nil
}

// SetOrderChannels records how the worker and support messages went out.
func (s *Store) SetOrderChannels(ctx context.Context, reference, workerChannel, supportChannel string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE order_slips SET worker_channel = $2, support_channel = $3 WHERE reference = $1`,
		reference, workerChannel, supportChannel)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOrders returns the newest slips first.
func (s *Store) ListOrders(ctx context.Context) ([]OrderSlip, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT reference, customer_name, customer_phone, COALESCE(worker_id, ''), COALESCE(catalog_phone_id, ''),
       phone_model, COALESCE(imei, ''), visit_window, status,
       mithril_account, COALESCE(worker_channel, ''), COALESCE(support_channel, ''), created_at
FROM order_slips
ORDER BY created_at DESC
LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderSlip
	for rows.Next() {
		var order OrderSlip
		var account sql.NullBool
		if err := rows.Scan(&order.Reference, &order.Name, &order.Phone, &order.WorkerID, &order.CatalogPhoneID, &order.PhoneModel, &order.IMEI, &order.Visit, &order.Status, &account, &order.WorkerChannel, &order.SupportChannel, &order.CreatedAt); err != nil {
			return nil, err
		}
		if account.Valid {
			value := account.Bool
			order.MithrilAccount = &value
		}
		out = append(out, order)
	}
	if out == nil {
		out = []OrderSlip{}
	}
	return out, rows.Err()
}

func newReference() string {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "P2O-LOCAL"
	}
	for i, b := range buf {
		buf[i] = orderAlphabet[int(b)%len(orderAlphabet)]
	}
	return "P2O-" + string(buf)
}

// ErrOrderUnavailable is returned when the order table cannot be used.
var ErrOrderUnavailable = errors.New("order store unavailable")
