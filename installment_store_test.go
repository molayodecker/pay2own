package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFindDemoInstallmentIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	db, err := openDatabase(dsn)
	if err != nil {
		t.Fatal("could not connect to test database")
	}
	defer db.Close()

	if _, err := db.ExecContext(context.Background(),
		`UPDATE demo_installments SET status = 'unpaid' WHERE id = 'demo-001'`); err != nil {
		t.Fatalf("reset demo row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := findDemoInstallment(ctx, db, "demo-001")
	if err != nil {
		t.Fatalf("find demo installment: %v", err)
	}

	want := demoInstallment()
	if got != want {
		t.Errorf("installment = %+v; want %+v", got, want)
	}
}

func TestMarkDemoInstallmentPaidIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := openDatabase(dsn)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(),
			`UPDATE demo_installments SET status = 'unpaid' WHERE id = 'demo-001'`)
	})

	got, err := markDemoInstallmentPaid(ctx, db, "demo-001")
	if err != nil {
		t.Fatalf("markDemoInstallmentPaid: %v", err)
	}
	if got.Status != "paid" {
		t.Errorf("status = %q; want %q", got.Status, "paid")
	}
}
