package main

import (
	"context"
	"database/sql"
)

type NextInstallment struct {
	IMEI        string    `json:"imei"`
	Sequence    int       `json:"sequence"`
	DueDate     string    `json:"due_date"`
	Currency    string    `json:"currency"`
	AmountMinor int64     `json:"amount_minor"`
	Status      string    `json:"status"`
}

func findNextInstallment(ctx context.Context, db *sql.DB, imei string) (NextInstallment, error) {
	var n NextInstallment

	err := db.QueryRowContext(ctx, `
		SELECT d.imei, i.sequence, to_char(i.due_date, 'YYYY-MM-DD'), l.currency, i.amount_minor, i.status
		FROM devices d
		JOIN loans l        ON l.device_id = d.id
		JOIN installments i ON i.loan_id = l.id
		WHERE d.imei = $1
		  AND l.status = 'active'
		  AND i.status = 'unpaid'
		ORDER BY i.sequence
		LIMIT 1
	`, imei).Scan(&n.IMEI, &n.Sequence, &n.DueDate, &n.Currency, &n.AmountMinor, &n.Status)

	return n, err
}