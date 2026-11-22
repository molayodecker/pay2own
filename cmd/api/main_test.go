package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAndroidRootRedirectsToAdmin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://android.pay2own.app/", nil)
	req.Host = "android.pay2own.app"
	rec := httptest.NewRecorder()
	if !redirectShopRoot(rec, req) {
		t.Fatal("expected a redirect")
	}
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/" {
		t.Fatalf("status %d location %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOtherHostsStayOnTheRoot(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://pay2own.app/", nil)
	req.Host = "pay2own.app"
	rec := httptest.NewRecorder()
	if redirectShopRoot(rec, req) {
		t.Fatal("pay2own.app root should not redirect")
	}
}

func TestAndroidHealthIsNotRedirected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://android.pay2own.app/health", nil)
	req.Host = "android.pay2own.app"
	rec := httptest.NewRecorder()
	if redirectShopRoot(rec, req) {
		t.Fatal("health should not redirect")
	}
}
