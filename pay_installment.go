package main

import (
	"context"
	"database/sql"
)

// payNextInstallment marks the device's earliest unpaid installment as paid.
// TEST ONLY: real payments must come from a verified mobile-money notification.
func payNextInstallment(ctx context.Context, db *sql.DB, imei string) (NextInstallment, error) {
	const query = `
	WITH next AS (
		SELECT i.id, d.imei, l.currency
		FROM devices d
		JOIN loans l ON l.device_id = d.id
		JOIN installments i ON i.loan_id = l.id
		WHERE d.imei = $1 AND l.status = 'active' AND i.status = 'unpaid'
		ORDER BY i.sequence
		LIMIT 1
		FOR UPDATE OF i
	)
	UPDATE installments i
	SET status = 'paid', paid_at = now()
	FROM next
	WHERE i.id = next.id
	RETURNING next.imei, i.sequence, to_char(i.due_date, 'YYYY-MM-DD'), next.currency, i.amount_minor, i.status`

	var n NextInstallment
	err := db.QueryRowContext(ctx, query, imei).Scan(
		&n.IMEI, &n.Sequence, &n.DueDate, &n.Currency, &n.AmountMinor, &n.Status,
	)
	return n, err
}