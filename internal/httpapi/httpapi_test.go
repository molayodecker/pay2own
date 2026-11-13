package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"pay2own.com/backend/internal/store"
)

type fakeInstallments struct {
	next store.NextInstallment
	err  error
}

func (f fakeInstallments) NextInstallment(_ context.Context, imei string) (store.NextInstallment, error) {
	n := f.next
	n.IMEI = imei
	return n, f.err
}

func (f fakeInstallments) PayNextInstallment(_ context.Context, imei string) (store.NextInstallment, error) {
	n := f.next
	n.IMEI = imei
	n.Status = "paid"
	return n, f.err
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

var sample = store.NextInstallment{Sequence: 1, DueDate: "2026-11-20", Currency: "GHS", AmountMinor: 30000, Status: "unpaid"}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealth(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog), http.MethodGet, "/health")
	if rec.Code != http.StatusOK || rec.Body.String() != "Pay2Own backend is running\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestNextInstallmentOK(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog), http.MethodGet, "/api/devices/000000000000001/next-installment")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got store.NextInstallment
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Sequence != 1 || got.Status != "unpaid" || got.IMEI != "000000000000001" {
		t.Fatalf("got %+v", got)
	}
}

func TestBadIMEI(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog), http.MethodGet, "/api/devices/123/next-installment")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: store.ErrNotFound}, quietLog), http.MethodGet, "/api/devices/000000000000001/next-installment")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDatabaseErrorIsHidden(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: errors.New("connection refused")}, quietLog), http.MethodGet, "/api/devices/000000000000001/next-installment")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "Internal server error\n" {
		t.Fatalf("leaked details: %q", rec.Body.String())
	}
}

func TestPayRoute(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog), http.MethodPost, "/api/devices/000000000000001/next-installment/pay")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got store.NextInstallment
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Status != "paid" {
		t.Fatalf("got %+v", got)
	}
}

func TestPayRouteRejectsGet(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog), http.MethodGet, "/api/devices/000000000000001/next-installment/pay")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}