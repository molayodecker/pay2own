package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fixtureNextInstallment(_ context.Context, imei string) (NextInstallment, error) {
	return NextInstallment{IMEI: imei, Sequence: 1, DueDate: "2026-11-20", Currency: "GHS", AmountMinor: 30000, Status: "unpaid"}, nil
}

func fixturePayNextInstallment(_ context.Context, imei string) (NextInstallment, error) {
	return NextInstallment{IMEI: imei, Sequence: 1, DueDate: "2026-11-20", Currency: "GHS", AmountMinor: 30000, Status: "paid"}, nil
}

func TestHealthRoute(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	newRouter(fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}
	want := "Pay2Own backend is running\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q; want %q", got, want)
	}
}

func TestNextInstallmentRejectsBadIMEI(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/devices/123/next-installment", nil)
	recorder := httptest.NewRecorder()

	newRouter(fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", recorder.Code)
	}
}

func TestPayNextInstallmentRoute(t *testing.T) {
	router := newRouter(fixtureNextInstallment, fixturePayNextInstallment)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/000000000000001/next-installment/pay", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got NextInstallment
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "paid" || got.Sequence != 1 {
		t.Fatalf("got #%d %q, want #1 paid", got.Sequence, got.Status)
	}
}