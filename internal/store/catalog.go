package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrOutOfStock means no available handset is left for that model.
var ErrOutOfStock = errors.New("out of stock")

// ErrIMEITaken means that IMEI is already recorded on a phone.
var ErrIMEITaken = errors.New("imei taken")

// ErrBadIMEI means an IMEI is not 15 digits.
var ErrBadIMEI = errors.New("bad imei")

// ErrCatalogReserved protects physical handsets held by open orders.
var ErrCatalogReserved = errors.New("catalog has reserved units")

// CatalogUnit is one physical handset on a catalog phone.
type CatalogUnit struct {
	IMEI   string
	Status string
}

// CatalogPhone is a model shown on the website. It is not a financed handset.
// IMEIs are the available handsets the admin wants saved.
type CatalogPhone struct {
	ID          string
	Name        string
	Short       string
	Description string
	Image       string
	PriceMinor  sql.NullInt64
	InStock     bool
	Available   int
	IMEIs       []string
	Units       []CatalogUnit
	SortOrder   int
}

func (s *Store) ListCatalog(ctx context.Context) ([]CatalogPhone, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.name, p.short_name, p.description, p.image_path, p.price_minor, p.sort_order,
       (SELECT COUNT(*) FROM catalog_phone_units u WHERE u.catalog_phone_id = p.id AND u.status = 'available')
FROM catalog_phones p
ORDER BY p.sort_order, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	phones := []CatalogPhone{}
	for rows.Next() {
		var phone CatalogPhone
		if err := rows.Scan(&phone.ID, &phone.Name, &phone.Short, &phone.Description, &phone.Image, &phone.PriceMinor, &phone.SortOrder, &phone.Available); err != nil {
			return nil, err
		}
		phone.InStock = phone.Available > 0
		phones = append(phones, phone)
	}
	return phones, rows.Err()
}

func (s *Store) GetCatalog(ctx context.Context, id string) (CatalogPhone, error) {
	var phone CatalogPhone
	err := s.db.QueryRowContext(ctx, `
SELECT p.id, p.name, p.short_name, p.description, p.image_path, p.price_minor, p.sort_order,
       (SELECT COUNT(*) FROM catalog_phone_units u WHERE u.catalog_phone_id = p.id AND u.status = 'available')
FROM catalog_phones p WHERE p.id = $1`, id).Scan(&phone.ID, &phone.Name, &phone.Short, &phone.Description, &phone.Image, &phone.PriceMinor, &phone.SortOrder, &phone.Available)
	if errors.Is(err, sql.ErrNoRows) {
		return phone, ErrNotFound
	}
	if err != nil {
		return phone, err
	}
	phone.InStock = phone.Available > 0
	rows, err := s.db.QueryContext(ctx, `
SELECT imei, status FROM catalog_phone_units WHERE catalog_phone_id = $1 ORDER BY id`, id)
	if err != nil {
		return phone, err
	}
	defer rows.Close()
	for rows.Next() {
		var unit CatalogUnit
		if err := rows.Scan(&unit.IMEI, &unit.Status); err != nil {
			return phone, err
		}
		phone.Units = append(phone.Units, unit)
		if unit.Status == "available" {
			phone.IMEIs = append(phone.IMEIs, unit.IMEI)
		}
	}
	if phone.Units == nil {
		phone.Units = []CatalogUnit{}
	}
	if phone.IMEIs == nil {
		phone.IMEIs = []string{}
	}
	return phone, rows.Err()
}

func (s *Store) SaveCatalog(ctx context.Context, phone CatalogPhone) (CatalogPhone, error) {
	imeis, err := normalizeIMEIs(phone.IMEIs)
	if err != nil {
		return CatalogPhone{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CatalogPhone{}, err
	}
	defer tx.Rollback()

	var price any
	if phone.PriceMinor.Valid {
		price = phone.PriceMinor.Int64
	}
	err = tx.QueryRowContext(ctx, `
INSERT INTO catalog_phones (id, name, short_name, description, image_path, price_minor, in_stock, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, false, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM catalog_phones))
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    short_name = EXCLUDED.short_name,
    description = EXCLUDED.description,
    image_path = EXCLUDED.image_path,
    price_minor = EXCLUDED.price_minor,
    updated_at = now()
RETURNING id`,
		phone.ID, phone.Name, phone.Short, phone.Description, phone.Image, price,
	).Scan(&phone.ID)
	if err != nil {
		return CatalogPhone{}, err
	}
	if err := replaceAvailableIMEIs(ctx, tx, phone.ID, imeis); err != nil {
		return CatalogPhone{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogPhone{}, err
	}
	return s.GetCatalog(ctx, phone.ID)
}

func (s *Store) DeleteCatalog(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_phones WHERE id = $1 FOR UPDATE`, id).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM catalog_phone_units WHERE catalog_phone_id = $1 AND status = 'reserved'
		UNION ALL
		SELECT 1 FROM order_slips WHERE catalog_phone_id = $1 AND status = 'open'
	)`, id).Scan(&active)
	if err != nil {
		return err
	}
	if active {
		return ErrCatalogReserved
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM catalog_phones WHERE id = $1`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func replaceAvailableIMEIs(ctx context.Context, tx *sql.Tx, phoneID string, imeis []string) error {
	wanted := map[string]struct{}{}
	for _, imei := range imeis {
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT catalog_phone_id FROM catalog_phone_units WHERE imei = $1`, imei).Scan(&owner)
		if err == nil && owner != phoneID {
			return ErrIMEITaken
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		wanted[imei] = struct{}{}
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO catalog_phone_units (catalog_phone_id, imei) VALUES ($1, $2)`, phoneID, imei); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT imei, status FROM catalog_phone_units WHERE catalog_phone_id = $1`, phoneID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var drop []string
	for rows.Next() {
		var imei, status string
		if err := rows.Scan(&imei, &status); err != nil {
			return err
		}
		if status != "available" {
			continue
		}
		if _, keep := wanted[imei]; !keep {
			drop = append(drop, imei)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, imei := range drop {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM catalog_phone_units WHERE catalog_phone_id = $1 AND imei = $2 AND status = 'available'`, phoneID, imei); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
UPDATE catalog_phones SET in_stock = EXISTS (
    SELECT 1 FROM catalog_phone_units WHERE catalog_phone_id = $1 AND status = 'available'
), updated_at = now() WHERE id = $1`, phoneID)
	return err
}

func normalizeIMEIs(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, value)
		if digits == "" {
			continue
		}
		if len(digits) != 15 {
			return nil, ErrBadIMEI
		}
		if _, ok := seen[digits]; ok {
			continue
		}
		seen[digits] = struct{}{}
		out = append(out, digits)
	}
	return out, nil
}
