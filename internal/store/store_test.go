package store

import (
	"context"
	"os"
	"testing"
)

const testIMEI = "000000000000001"

// openTestStore connects to the test database, or skips if it isn't configured.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

// resetInstallments marks every installment for loan 1 unpaid, now and after the test.
func resetInstallments(t *testing.T, st *Store) {
	t.Helper()
	reset := func() {
		_, err := st.db.ExecContext(context.Background(),
			`UPDATE installments SET status = 'unpaid', paid_at = NULL WHERE loan_id = 1`)
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	reset()
	t.Cleanup(reset)
}

func TestNextInstallmentIntegration(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)

	got, err := st.NextInstallment(context.Background(), testIMEI)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := NextInstallment{IMEI: testIMEI, Sequence: 1, DueDate: "2026-11-20", Currency: "GHS", AmountMinor: 30000, Status: "unpaid"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNextInstallmentUnknownIMEI(t *testing.T) {
	st := openTestStore(t)

	_, err := st.NextInstallment(context.Background(), "999999999999999")
	if err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPayNextInstallmentIntegration(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()

	paid, err := st.PayNextInstallment(ctx, testIMEI)
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Sequence != 1 || paid.Status != "paid" {
		t.Fatalf("paid = %+v, want #1 paid", paid)
	}

	next, err := st.NextInstallment(ctx, testIMEI)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next.Sequence != 2 || next.DueDate != "2026-11-27" {
		t.Fatalf("next = %+v, want #2 due 2026-11-27", next)
	}
}

func TestDeviceTokenLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	first, err := st.IssueDeviceToken(ctx, testIMEI)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if imei, err := st.DeviceIMEIByToken(ctx, first); err != nil || imei != testIMEI {
		t.Fatalf("lookup = %q, %v; want %s", imei, err, testIMEI)
	}

	second, err := st.IssueDeviceToken(ctx, testIMEI)
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := st.DeviceIMEIByToken(ctx, first); err != ErrNotFound {
		t.Fatalf("old token still works: err = %v", err)
	}
	if imei, err := st.DeviceIMEIByToken(ctx, second); err != nil || imei != testIMEI {
		t.Fatalf("new token lookup = %q, %v", imei, err)
	}
	if _, err := st.IssueDeviceToken(ctx, "999999999999999"); err != ErrNotFound {
		t.Fatalf("unknown device: err = %v, want ErrNotFound", err)
	}
}
