package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fixtureInstallmentReader(
	_ context.Context,
	_ string,
) (Installment, error) {
	return demoInstallment(), nil
}

func fixtureInstallmentPayer(ctx context.Context, id string) (Installment, error) {
	installment := demoInstallment()
	installment.Status = "paid"
	return installment, nil
}

func TestInstallmentHandler(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/demo/installment",
		nil,
	)
	recorder := httptest.NewRecorder()
	installmentHandler(fixtureInstallmentReader)(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}

	contentType := recorder.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", contentType)
	}

	var got Installment
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	want := Installment{
		ID:          "demo-001",
		Currency:    "GHS",
		AmountMinor: 10000,
		Status:      "unpaid",
		Demo:        true,
	}

	if got != want {
		t.Errorf("response = %+v; want %+v", got, want)
	}
}

func TestHealthRoute(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	newRouter(fixtureInstallmentReader, fixtureInstallmentPayer, fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}

	want := "Pay2Own backend is running\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q; want %q", got, want)
	}
}

func TestInstallmentRoute(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/demo/installment",
		nil,
	)

	recorder := httptest.NewRecorder()

	newRouter(fixtureInstallmentReader, fixtureInstallmentPayer, fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}

	var got Installment
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.ID != "demo-001" || !got.Demo {
		t.Errorf("unexpected demo response: %+v", got)
	}
}

func TestInstallmentRouteRejectsPost(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/demo/installment",
		nil,
	)
	recorder := httptest.NewRecorder()

	newRouter(fixtureInstallmentReader, fixtureInstallmentPayer, fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", recorder.Code)
	}
}

func TestDemoInstallment(t *testing.T) {
	got := demoInstallment()
	want := Installment{
		ID:          "demo-001",
		Currency:    "GHS",
		AmountMinor: 10000,
		Status:      "unpaid",
		Demo:        true,
	}
	if got != want {
		t.Errorf("demoInstallment() = %+v; want %+v", got, want)
	}
}

func TestInstallmentHandlerReaderFailure(t *testing.T) {
	failingReader := func(
		_ context.Context,
		_ string,
	) (Installment, error) {
		return Installment{}, errors.New("simulated database failure")
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/demo/installment",
		nil,
	)
	recorder := httptest.NewRecorder()

	installmentHandler(failingReader)(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", recorder.Code)
	}

	want := "Internal server error\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q; want %q", got, want)
	}
}

func TestPayInstallmentHandler(t *testing.T) {
	fakePay := func(ctx context.Context, id string) (Installment, error) {
		installment := demoInstallment()
		installment.Status = "paid"
		return installment, nil
	}

	request := httptest.NewRequest(http.MethodPost, "/api/demo/installment/pay", nil)
	recorder := httptest.NewRecorder()

	payInstallmentHandler(fakePay)(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", recorder.Code)
	}

	var got Installment
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "paid" {
		t.Errorf("status = %q; want %q", got.Status, "paid")
	}
}

func TestInstallmentHandlerNotFound(t *testing.T) {
	missingReader := func(_ context.Context, _ string) (Installment, error) {
		return Installment{}, sql.ErrNoRows
	}

	request := httptest.NewRequest(http.MethodGet, "/api/demo/installment", nil)
	recorder := httptest.NewRecorder()

	installmentHandler(missingReader)(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", recorder.Code)
	}
}

func fixtureNextInstallment(_ context.Context, imei string) (NextInstallment, error) {
	return NextInstallment{IMEI: imei, Sequence: 1, Currency: "GHS", AmountMinor: 30000, Status: "unpaid"}, nil
}
func fixturePayNextInstallment(ctx context.Context, imei string) (NextInstallment, error) {
	return NextInstallment{
		IMEI:        imei,
		Sequence:    1,
		DueDate:     "2026-11-20",
		Currency:    "GHS",
		AmountMinor: 30000,
		Status:      "paid",
	}, nil
}

func TestNextInstallmentRejectsBadIMEI(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/devices/123/next-installment", nil)
	recorder := httptest.NewRecorder()

	newRouter(fixtureInstallmentReader, fixtureInstallmentPayer, fixtureNextInstallment, fixturePayNextInstallment).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", recorder.Code)
	}
}

func TestPayNextInstallmentRoute(t *testing.T) {
	router := newRouter(fixtureInstallmentReader, fixtureInstallmentPayer, fixtureNextInstallment, fixturePayNextInstallment)
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