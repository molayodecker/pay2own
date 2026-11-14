package store

import (
	"context"
	"errors"
	"os"
	"strings"
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

// resetInstallments clears payments and marks every installment for loan 1 unpaid, now and after the test.
func resetInstallments(t *testing.T, st *Store) {
	t.Helper()
	reset := func() {
		ctx := context.Background()
		if _, err := st.db.ExecContext(ctx, `DELETE FROM payments`); err != nil {
			t.Fatalf("reset payments: %v", err)
		}
		if _, err := st.db.ExecContext(ctx,
			`UPDATE installments SET status = 'unpaid', paid_at = NULL WHERE loan_id = 1`); err != nil {
			t.Fatalf("reset installments: %v", err)
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
func TestStartPayment(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()

	p, err := st.StartPayment(ctx, testIMEI)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Reference, "p2o_") || p.AmountMinor != 30000 || p.Currency != "GHS" {
		t.Fatalf("unexpected payment: %+v", p)
	}
	if p.CustomerID != 1 || p.Provider != "mtn" || p.Phone == "" {
		t.Fatalf("missing customer details: %+v", p)
	}

	if _, err := st.StartPayment(ctx, testIMEI); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("second start: got %v, want ErrAlreadyPending", err)
	}

	if _, err := st.StartPayment(ctx, "999999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: got %v, want ErrNotFound", err)
	}
}

func TestCompletePayment(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()

	p, err := st.StartPayment(ctx, testIMEI)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CompletePayment(ctx, p.Reference, 29999, "GHS"); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("wrong amount: got %v", err)
	}
	if err := st.CompletePayment(ctx, p.Reference, p.AmountMinor, "GHS"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompletePayment(ctx, p.Reference, p.AmountMinor, "GHS"); err != nil {
		t.Fatalf("second call should be a no-op: %v", err)
	}

	next, err := st.NextInstallment(ctx, testIMEI)
	if err != nil || next.Sequence != 2 {
		t.Fatalf("after payment, next = %+v, %v; want #2", next, err)
	}
	if err := st.CompletePayment(ctx, "p2o_unknown", 1, "GHS"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown reference: got %v", err)
	}
}
