package main

import (
	"context"
	"database/sql"
)

func findDemoInstallment(
	ctx context.Context,
	db *sql.DB,
	id string,
) (Installment, error) {
	var installment Installment

	err := db.QueryRowContext(ctx, `
        SELECT id, currency, amount_minor, status, demo
        FROM demo_installments
        WHERE id = $1 AND demo = TRUE
    `, id).Scan(
		&installment.ID,
		&installment.Currency,
		&installment.AmountMinor,
		&installment.Status,
		&installment.Demo,
	)

	if err != nil {
		return Installment{}, err
	}

	return installment, nil
}

func markDemoInstallmentPaid(ctx context.Context, db *sql.DB, id string) (Installment, error) {
	var installment Installment

	err := db.QueryRowContext(ctx, `
        UPDATE demo_installments
        SET status = 'paid'
        WHERE id = $1 AND demo = TRUE
        RETURNING id, currency, amount_minor, status, demo
    `, id).Scan(
		&installment.ID,
		&installment.Currency,
		&installment.AmountMinor,
		&installment.Status,
		&installment.Demo,
	)

	return installment, err
}
