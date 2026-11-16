package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pay2own.com/backend/internal/paystack"
	"pay2own.com/backend/internal/store"
)

const (
	goodToken = "good-token"
	phoneIMEI = "000000000000001"
	otherIMEI = "000000000000002"
)

type fakeInstallments struct {
	next             store.NextInstallment
	err              error
	summary          *store.LoanSummary
	payments         []store.PaymentRecord
	gotCount         *int
	state            *store.PaymentRecord
	restrictionState string
	restrictionErr   error
	releaseErr       error
}

func (f fakeInstallments) NextInstallment(_ context.Context, imei string) (store.NextInstallment, error) {
	n := f.next
	n.IMEI = imei
	return n, f.err
}

func (f fakeInstallments) DeviceIMEIByToken(_ context.Context, token string) (string, error) {
	if token == goodToken {
		return phoneIMEI, nil
	}
	return "", store.ErrNotFound
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

var sample = store.NextInstallment{Sequence: 1, DueDate: "2026-11-20", Currency: "GHS", AmountMinor: 30000, Status: "unpaid"}

func do(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func nextPath(imei string) string { return "/api/devices/" + imei + "/next-installment" }

func TestHealth(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog, Options{}), http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "Pay2Own backend is running\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestNextInstallmentOK(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{}), http.MethodGet, nextPath(phoneIMEI), goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got store.NextInstallment
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Sequence != 1 || got.Status != "unpaid" || got.IMEI != phoneIMEI {
		t.Fatalf("got %+v", got)
	}
}

func TestMissingTokenIs401(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{}), http.MethodGet, nextPath(phoneIMEI), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWrongTokenIs401(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{}), http.MethodGet, nextPath(phoneIMEI), "stolen-guess")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestOtherPhonesIMEIIs403(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{}), http.MethodGet, nextPath(otherIMEI), goodToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestBadIMEI(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog, Options{}), http.MethodGet, nextPath("123"), goodToken)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 403 or 400", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: store.ErrNotFound}, quietLog, Options{}), http.MethodGet, nextPath(phoneIMEI), goodToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDatabaseErrorIsHidden(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: errors.New("connection refused")}, quietLog, Options{}), http.MethodGet, nextPath(phoneIMEI), goodToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "Internal server error\n" {
		t.Fatalf("leaked details: %q", rec.Body.String())
	}
}

func TestLogsHideIMEIAndToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewRouter(fakeInstallments{}, log, Options{})

	req := httptest.NewRequest(http.MethodGet, nextPath(phoneIMEI), nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if !strings.Contains(out, `"route":"GET /api/devices/{imei}/next-installment"`) {
		t.Fatalf("route missing from log: %s", out)
	}
	if strings.Contains(out, phoneIMEI) || strings.Contains(out, goodToken) {
		t.Fatalf("log leaked IMEI or token: %s", out)
	}
}

func (f fakeInstallments) StartPayment(_ context.Context, imei string, count int) (store.PendingPayment, error) {
	if f.gotCount != nil {
		*f.gotCount = count
	}
	if imei != phoneIMEI {
		return store.PendingPayment{}, store.ErrNotFound
	}
	return store.PendingPayment{Reference: "p2o_test", Currency: "GHS", AmountMinor: 30000,
		CustomerID: 1, Phone: "0551234987", Provider: "mtn"}, nil
}

func (f fakeInstallments) FailPayment(context.Context, string) error {
	if f.state != nil {
		f.state.Status = "failed"
	}
	return nil
}

func (f fakeInstallments) CompletePayment(_ context.Context, reference string, amount int64, currency string) (bool, error) {
	if f.state != nil {
		f.state.Status = "success"
		f.state.AmountMinor = amount
		f.state.Currency = currency
		f.state.Reference = reference
	}
	return true, nil
}

func (f fakeInstallments) LoanSummary(context.Context, string) (store.LoanSummary, error) {
	if f.summary == nil {
		return store.LoanSummary{}, store.ErrNotFound
	}
	return *f.summary, nil
}

func (f fakeInstallments) ListPayments(context.Context, string) ([]store.PaymentRecord, error) {
	if f.payments == nil {
		return []store.PaymentRecord{}, f.err
	}
	return f.payments, nil
}

func (f fakeInstallments) Payment(_ context.Context, _, ref string) (store.PaymentRecord, error) {
	if f.state != nil && (f.state.Reference == ref || f.state.Reference == "") {
		return *f.state, nil
	}
	for _, p := range f.payments {
		if p.Reference == ref {
			return p, nil
		}
	}
	return store.PaymentRecord{}, store.ErrNotFound
}

func (f fakeInstallments) SavePushToken(context.Context, string, string) error { return nil }

type fakeCharger struct {
	err error
	got paystack.ChargeRequest
}

func (c *fakeCharger) Charge(_ context.Context, req paystack.ChargeRequest) (paystack.ChargeResponse, error) {
	c.got = req
	return paystack.ChargeResponse{Status: "pay_offline", DisplayText: "Authorize payment on your phone"}, c.err
}

func (c *fakeCharger) Verify(context.Context, string) (paystack.Verification, error) {
	return paystack.Verification{Status: "success", AmountMinor: 30000, Currency: "GHS"}, nil
}

func (c *fakeCharger) ValidSignature(_ []byte, sig string) bool { return sig == "good-sig" }

func paymentRouter(ch Charger) http.Handler {
	return NewRouter(fakeInstallments{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Charger: ch})
}

func postPayment(h http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/devices/"+phoneIMEI+"/payments", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStartPaymentOK(t *testing.T) {
	ch := &fakeCharger{}
	rec := postPayment(paymentRouter(ch))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Authorize payment on your phone") { t.Fatalf("missing Paystack instructions: %s", rec.Body.String()) }
	if ch.got.Email != "customer-1@pay2own.com" || ch.got.AmountMinor != 30000 || ch.got.Provider != "mtn" {
		t.Fatalf("bad charge request: %+v", ch.got)
	}
}

func TestStartPaymentProviderDown(t *testing.T) {
	rec := postPayment(paymentRouter(&fakeCharger{err: errors.New("boom")}))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestPaymentsOffWithoutCharger(t *testing.T) {
	if rec := postPayment(paymentRouter(nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func postWebhook(h http.Handler, sig, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paystack", strings.NewReader(body))
	req.Header.Set("X-Paystack-Signature", sig)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	body := `{"event":"charge.success","data":{"reference":"p2o_test"}}`
	if code := postWebhook(paymentRouter(&fakeCharger{}), "forged", body); code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
}

func TestLoanSummaryRequiresToken(t *testing.T) {
	masked := "055•••4987"
	h := NewRouter(fakeInstallments{summary: &store.LoanSummary{
		CustomerName: "Ama", PhoneMasked: masked, AccountStatus: "up_to_date", Terms: "Pay2Own",
	}}, quietLog, Options{})
	if rec := do(t, h, http.MethodGet, "/api/devices/"+phoneIMEI+"/loan", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	rec := do(t, h, http.MethodGet, "/api/devices/"+phoneIMEI+"/loan", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "0551234987") {
		t.Fatalf("response contained the full number: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), masked) {
		t.Fatalf("masked number missing: %s", rec.Body.String())
	}
}

func TestStartPaymentCount(t *testing.T) {
	var got int
	h := NewRouter(&countingFake{got: &got}, quietLog, Options{Charger: &fakeCharger{}})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/"+phoneIMEI+"/payments", strings.NewReader(`{"installments":3}`))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || got != 3 {
		t.Fatalf("status %d count %d", rec.Code, got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/devices/"+phoneIMEI+"/payments", strings.NewReader(`{"pay_balance":true}`))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || got != 0 {
		t.Fatalf("balance status %d count %d", rec.Code, got)
	}
}

type countingFake struct {
	fakeInstallments
	got *int
}

func (f *countingFake) StartPayment(ctx context.Context, imei string, count int) (store.PendingPayment, error) {
	*f.got = count
	return f.fakeInstallments.StartPayment(ctx, imei, count)
}

func TestPaymentPollCompletes(t *testing.T) {
	state := &store.PaymentRecord{Reference: "p2o_test", Status: "pending", Currency: "GHS", AmountMinor: 30000, Sequences: []int{1}}
	h := NewRouter(fakeInstallments{state: state}, quietLog, Options{Charger: &fakeCharger{}})
	rec := do(t, h, http.MethodGet, "/api/devices/"+phoneIMEI+"/payments/p2o_test", goodToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"success"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestPushTokenRejected(t *testing.T) {
	h := NewRouter(fakeInstallments{}, quietLog, Options{})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/"+phoneIMEI+"/push-token", strings.NewReader(`{"token":"short"}`))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookAcceptsSignedSuccess(t *testing.T) {
	body := `{"event":"charge.success","data":{"reference":"p2o_test"}}`
	if code := postWebhook(paymentRouter(&fakeCharger{}), "good-sig", body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func (f fakeInstallments) DeviceRestrictionState(context.Context, string) (string, error) {
	return f.restrictionState, f.restrictionErr
}

func (f fakeInstallments) ConfirmRelease(context.Context, string) error {
	return f.releaseErr
}

func TestRestrictionState(t *testing.T) {
	h := NewRouter(fakeInstallments{restrictionState: "active"}, quietLog, Options{})
	rec := do(t, h, http.MethodGet, "/api/devices/"+phoneIMEI+"/restriction", goodToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"active"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestReleaseConfirmed(t *testing.T) {
	h := NewRouter(fakeInstallments{}, quietLog, Options{})
	rec := do(t, h, http.MethodPost, "/api/devices/"+phoneIMEI+"/release-confirmed", goodToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	h = NewRouter(fakeInstallments{releaseErr: store.ErrNotReleasable}, quietLog, Options{})
	rec = do(t, h, http.MethodPost, "/api/devices/"+phoneIMEI+"/release-confirmed", goodToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}

	rec = do(t, h, http.MethodPost, "/api/devices/"+phoneIMEI+"/release-confirmed", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

type receiptStore struct { fakeInstallments }

func (f receiptStore) PaymentContact(context.Context, string) (store.PaymentContact, error) {
	return store.PaymentContact{Phone: "0551234987", AmountMinor: 30000, Currency: "GHS"}, nil
}

type receiptSender struct { sms int }

func (s *receiptSender) SendPush(context.Context, string, string, string) error { return nil }
func (s *receiptSender) SendSMS(context.Context, string, string) error {
	s.sms++
	return nil
}

func TestPollSendsPaymentReceiptOnlyOnFirstCompletion(t *testing.T) {
	state := &store.PaymentRecord{Reference: "p2o_test", Status: "pending", Currency: "GHS", AmountMinor: 30000}
	sender := &receiptSender{}
	h := NewRouter(receiptStore{fakeInstallments{state: state}}, quietLog, Options{
		Charger: &fakeCharger{}, Notify: sender,
	})
	path := "/api/devices/" + phoneIMEI + "/payments/p2o_test"
	if rec := do(t, h, http.MethodGet, path, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("poll response %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, path, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("repeat poll response %d", rec.Code)
	}
	if sender.sms != 1 {
		t.Fatalf("receipt sent %d times, expected exactly 1", sender.sms)
	}
}
