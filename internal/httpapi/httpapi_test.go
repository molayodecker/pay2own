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

	"pay2own.com/backend/internal/store"
)

const (
	goodToken = "good-token"
	phoneIMEI = "000000000000001"
	otherIMEI = "000000000000002"
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
	rec := do(t, NewRouter(fakeInstallments{}, quietLog, Options{EnableTestPay: true}), http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "Pay2Own backend is running\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestNextInstallmentOK(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI), goodToken)
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
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWrongTokenIs401(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI), "stolen-guess")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestOtherPhonesIMEIIs403(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(otherIMEI), goodToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestPayNeedsToken(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodPost, nextPath(phoneIMEI)+"/pay", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestBadIMEI(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath("123"), goodToken)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 403 or 400", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: store.ErrNotFound}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI), goodToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDatabaseErrorIsHidden(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{err: errors.New("connection refused")}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI), goodToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "Internal server error\n" {
		t.Fatalf("leaked details: %q", rec.Body.String())
	}
}

func TestPayRoute(t *testing.T) {
	rec := do(t, NewRouter(fakeInstallments{next: sample}, quietLog, Options{EnableTestPay: true}), http.MethodPost, nextPath(phoneIMEI)+"/pay", goodToken)
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
	rec := do(t, NewRouter(fakeInstallments{}, quietLog, Options{EnableTestPay: true}), http.MethodGet, nextPath(phoneIMEI)+"/pay", goodToken)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestLogsHideIMEIAndToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := NewRouter(fakeInstallments{}, log, Options{EnableTestPay: true})

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

func TestPayRouteOffByDefault(t *testing.T) {
	h := NewRouter(fakeInstallments{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	req := httptest.NewRequest(http.MethodPost, nextPath(phoneIMEI)+"/pay", nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}
