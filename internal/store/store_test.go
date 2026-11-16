package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
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
		if _, err := st.db.ExecContext(ctx, `UPDATE loans SET status = 'active' WHERE id = 1`); err != nil {
			t.Fatalf("reset loan: %v", err)
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

	p, err := st.StartPayment(ctx, testIMEI, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Reference, "p2o_") || p.AmountMinor != 30000 || p.Currency != "GHS" {
		t.Fatalf("unexpected payment: %+v", p)
	}
	if p.CustomerID != 1 || p.Provider != "mtn" || p.Phone == "" {
		t.Fatalf("missing customer details: %+v", p)
	}

	if _, err := st.StartPayment(ctx, testIMEI, 1); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("second start: got %v, want ErrAlreadyPending", err)
	}

	if _, err := st.StartPayment(ctx, "999999999999999", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: got %v, want ErrNotFound", err)
	}
}

func TestCompletePayment(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()

	p, err := st.StartPayment(ctx, testIMEI, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CompletePayment(ctx, p.Reference, 29999, "GHS"); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("wrong amount: got %v", err)
	}
	fresh, err := st.CompletePayment(ctx, p.Reference, p.AmountMinor, "GHS")
	if err != nil || !fresh {
		t.Fatalf("complete: fresh=%v err=%v", fresh, err)
	}
	fresh, err = st.CompletePayment(ctx, p.Reference, p.AmountMinor, "GHS")
	if err != nil || fresh {
		t.Fatalf("second call should be a no-op: fresh=%v err=%v", fresh, err)
	}

	next, err := st.NextInstallment(ctx, testIMEI)
	if err != nil || next.Sequence != 2 {
		t.Fatalf("after payment, next = %+v, %v; want #2", next, err)
	}
	if _, err := st.CompletePayment(ctx, "p2o_unknown", 1, "GHS"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown reference: got %v", err)
	}
}

func TestCompletePaymentReleasesDeviceOnLastInstallment(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	resetInstallments(t, st)
	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'active', released_at = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'not_enrolled', released_at = NULL WHERE id = 1`)
	})

	for n := 1; n <= 4; n++ {
		p, err := st.StartPayment(ctx, testIMEI, 1)
		if err != nil {
			t.Fatalf("start payment %d: %v", n, err)
		}
		if _, err := st.CompletePayment(ctx, p.Reference, p.AmountMinor, p.Currency); err != nil {
			t.Fatalf("complete payment %d: %v", n, err)
		}
		want := "active"
		if n == 4 {
			want = "release_pending"
		}
		if got := deviceState(t, st, 1); got != want {
			t.Fatalf("after payment %d: state = %q, want %q", n, got, want)
		}
	}
}

func deviceState(t *testing.T, st *Store, id int64) string {
	t.Helper()
	var state string
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT restriction_state FROM devices WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestConfirmRelease(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const imei = "000000000000001"
	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'active', released_at = NULL WHERE imei = $1`, imei); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'not_enrolled', released_at = NULL WHERE imei = $1`, imei)
	})

	if err := st.ConfirmRelease(ctx, imei); !errors.Is(err, ErrNotReleasable) {
		t.Fatalf("active device: got %v, want ErrNotReleasable", err)
	}

	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'release_pending' WHERE imei = $1`, imei); err != nil {
		t.Fatal(err)
	}
	if err := st.ConfirmRelease(ctx, imei); err != nil {
		t.Fatalf("pending device: %v", err)
	}
	if err := st.ConfirmRelease(ctx, imei); err != nil {
		t.Fatalf("repeat confirm should be a no-op: %v", err)
	}
	if got, _ := st.DeviceRestrictionState(ctx, imei); got != "released" {
		t.Fatalf("state = %q, want released", got)
	}
	if err := st.ConfirmRelease(ctx, "000000000000999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown imei: got %v, want ErrNotFound", err)
	}
}

func TestSchedule(t *testing.T) {
	got := Schedule(100000, 3)
	if got[0] != 33333 || got[1] != 33333 || got[2] != 33334 {
		t.Fatalf("got %v", got)
	}
}

func TestCreateSale(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	imei := "000000000000099"
	t.Cleanup(func() {
		st.db.ExecContext(ctx, `DELETE FROM installments WHERE loan_id IN
			(SELECT l.id FROM loans l JOIN devices d ON d.id = l.device_id WHERE d.imei = $1)`, imei)
		st.db.ExecContext(ctx, `DELETE FROM loans WHERE device_id IN (SELECT id FROM devices WHERE imei = $1)`, imei)
		st.db.ExecContext(ctx, `DELETE FROM devices WHERE imei = $1`, imei)
		st.db.ExecContext(ctx, `DELETE FROM customers WHERE phone = '0559999999'`)
	})

	first := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	_, err := st.CreateSale(ctx, Sale{
		FullName: "Ama Test", Phone: "0559999999", Provider: "mtn",
		IMEI: imei, Model: "TECNO Spark", PriceMinor: 120000, DepositMinor: 20000,
		Weeks: 4, FirstDue: first,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := st.NextInstallment(ctx, imei)
	if err != nil || next.Sequence != 1 || next.AmountMinor != 25000 || next.DueDate != "2026-12-01" {
		t.Fatalf("got %+v, %v", next, err)
	}

	if _, err := st.CreateSale(ctx, Sale{FullName: "X", Phone: "0559999999", Provider: "mtn",
		IMEI: imei, Model: "dup", PriceMinor: 1000, Weeks: 1, FirstDue: first}); err == nil {
		t.Fatal("same IMEI twice should fail")
	}
}

func TestActivateDevice(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	imei := "000000000000098"
	t.Cleanup(func() {
		st.db.ExecContext(ctx, `DELETE FROM installments WHERE loan_id IN
			(SELECT l.id FROM loans l JOIN devices d ON d.id = l.device_id WHERE d.imei = $1)`, imei)
		st.db.ExecContext(ctx, `DELETE FROM loans WHERE device_id IN (SELECT id FROM devices WHERE imei = $1)`, imei)
		st.db.ExecContext(ctx, `DELETE FROM devices WHERE imei = $1`, imei)
		st.db.ExecContext(ctx, `DELETE FROM customers WHERE phone = '0559999998'`)
	})
	first := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	_, err := st.CreateSale(ctx, Sale{
		FullName: "Kofi Shop", Phone: "0559999998", Provider: "vod",
		IMEI: imei, Model: "BLU G33", PriceMinor: 150000, DepositMinor: 30000,
		Weeks: 4, FirstDue: first, Restrict: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.DeviceForAdmin(ctx, imei)
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomerName != "Kofi Shop" || got.RestrictionState != "active" || got.PriceMinor != 150000 {
		t.Fatalf("device %+v", got)
	}
	if err := st.ActivateDevice(ctx, imei); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'released' WHERE imei = $1`, imei); err != nil {
		t.Fatal(err)
	}
	if err := st.ActivateDevice(ctx, imei); !errors.Is(err, ErrNotActivatable) {
		t.Fatalf("released phone: %v", err)
	}
	if err := st.ActivateDevice(ctx, "000000000000097"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing phone: %v", err)
	}
}

func TestPaySeveralInstallments(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()

	p, err := st.StartPayment(ctx, testIMEI, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.AmountMinor != 60000 {
		t.Fatalf("amount %d, want 60000", p.AmountMinor)
	}
	if _, err := st.CompletePayment(ctx, p.Reference, p.AmountMinor, p.Currency); err != nil {
		t.Fatal(err)
	}
	next, err := st.NextInstallment(ctx, testIMEI)
	if err != nil || next.Sequence != 3 {
		t.Fatalf("next = %+v, %v; want #3", next, err)
	}
	list, err := st.ListPayments(ctx, testIMEI)
	if err != nil || len(list) != 1 || list[0].Status != "success" || len(list[0].Sequences) != 2 {
		t.Fatalf("payments %+v, %v", list, err)
	}

	if _, err := st.StartPayment(ctx, testIMEI, 9); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("too many: %v", err)
	}
}

func TestLoanSummary(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)

	sum, err := st.LoanSummary(context.Background(), testIMEI)
	if err != nil {
		t.Fatal(err)
	}
	if sum.CustomerName != "Test Customer" || sum.Model != "BLU G33" || sum.PriceMinor != 150000 || sum.DepositMinor != 30000 {
		t.Fatalf("summary identity %+v", sum)
	}
	if sum.PhoneMasked == "" || strings.Contains(sum.PhoneMasked, "300000") {
		t.Fatalf("phone leaked: %q", sum.PhoneMasked)
	}
	if sum.RemainingMinor != 120000 || sum.PaidMinor != 30000 || sum.InstallmentCount != 4 || sum.Next == nil || sum.Next.Sequence != 1 {
		t.Fatalf("amounts %+v", sum)
	}
	if sum.AccountStatus != AccountStatus(sum.LoanStatus, sum.Next.DueDate, TodayAccra(time.Now())) {
		t.Fatalf("status %s", sum.AccountStatus)
	}
	if !strings.Contains(sum.Terms, "Pay2Own") {
		t.Fatal("terms missing")
	}
}

func TestPendingPaymentDoesNotExpireWithoutReconciliation(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()
	first, err := st.StartPayment(ctx, testIMEI, 1)
	if err != nil { t.Fatal(err) }
	_, err = st.db.ExecContext(ctx, `UPDATE payments SET created_at = now() - interval '1 hour' WHERE reference = $1`, first.Reference)
	if err != nil { t.Fatal(err) }
	if _, err = st.StartPayment(ctx, testIMEI, 1); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("old unresolved payment must block a new charge: %v", err)
	}
}

func TestCannotActivatePaidOffDevice(t *testing.T) {
	st := openTestStore(t)
	resetInstallments(t, st)
	ctx := context.Background()
	_, err := st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'not_enrolled' WHERE imei = $1`, testIMEI)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() {
		st.db.ExecContext(ctx, `UPDATE devices SET restriction_state = 'not_enrolled' WHERE imei = $1`, testIMEI)
	})
	_, err = st.db.ExecContext(ctx, `UPDATE installments SET status = 'paid', paid_at = now() WHERE loan_id = 1`)
	if err != nil { t.Fatal(err) }
	_, err = st.db.ExecContext(ctx, `UPDATE loans SET status = 'paid_off' WHERE id = 1`)
	if err != nil { t.Fatal(err) }
	if err = st.ActivateDevice(ctx, testIMEI); !errors.Is(err, ErrNotActivatable) {
		t.Fatalf("paid off loan should not be restricted: %v", err)
	}
	if state, err := st.DeviceRestrictionState(ctx, testIMEI); err != nil || state != "not_enrolled" {
		t.Fatalf("unexpected state %q, %v", state, err)
	}
}
