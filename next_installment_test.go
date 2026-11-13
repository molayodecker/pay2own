package main

import (
	"context"
	"os"
	"testing"
)

func TestFindNextInstallmentIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := openDatabase(dsn)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer db.Close()

	got, err := findNextInstallment(context.Background(), db, "000000000000001")
	if err != nil {
		t.Fatalf("findNextInstallment: %v", err)
	}

	if got.Sequence != 1 || got.AmountMinor != 30000 || got.Status != "unpaid" {
		t.Errorf("unexpected installment: %+v", got)
	}
	if got.DueDate != "2026-11-20" {
		t.Errorf("due date = %q; want 2026-11-20", got.DueDate)
	}
}

func TestPayNextInstallmentIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := openDatabase(dsn)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	reset := func() {
		_, err := db.ExecContext(ctx,
			`UPDATE installments SET status = 'unpaid', paid_at = NULL WHERE loan_id = 1`)
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	reset()
	t.Cleanup(reset)

	paid, err := payNextInstallment(ctx, db, "000000000000001")
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Sequence != 1 || paid.Status != "paid" {
		t.Fatalf("got sequence %d status %q, want 1 paid", paid.Sequence, paid.Status)
	}

	next, err := findNextInstallment(ctx, db, "000000000000001")
	if err != nil {
		t.Fatalf("find next: %v", err)
	}
	if next.Sequence != 2 || next.DueDate != "2026-11-27" {
		t.Fatalf("got #%d due %s, want #2 due 2026-11-27", next.Sequence, next.DueDate)
	}
}