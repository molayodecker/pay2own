package mithril

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasAccountUsesTheConfiguredHost(t *testing.T) {
	var gotHost, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotToken = r.Header.Get("x-pay2own-token")
		if r.URL.Path != "/pay2own/workers/lookup" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["phone"] != "0241234567" || body["workerId"] != "IC-12" {
			t.Fatalf("body %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"account":true}`))
	}))
	defer srv.Close()

	ok, err := (Client{BaseURL: srv.URL, Token: "local-token", HTTP: srv.Client()}).HasAccount(context.Background(), "0241234567", "IC-12")
	if err != nil || !ok {
		t.Fatalf("account=%v err=%v", ok, err)
	}
	if gotToken != "local-token" || gotHost == "" {
		t.Fatalf("host %q token %q", gotHost, gotToken)
	}
}

func TestAPIBaseFollowsTheEnvironment(t *testing.T) {
	if got := APIBase("", ""); got != "http://127.0.0.1:4000" {
		t.Fatalf("local base %s", got)
	}
	if got := APIBase("production", ""); got != "https://api.tryinstaclean.com" {
		t.Fatalf("production base %s", got)
	}
	if got := APIBase("production", "http://127.0.0.1:4000/"); got != "http://127.0.0.1:4000" {
		t.Fatalf("override %s", got)
	}
	if IntakeToken("production", "") != "" {
		t.Fatal("production must not invent a token")
	}
	if IntakeToken("", "") != "pay2own-local-dev" {
		t.Fatal("local token default")
	}
}

func TestHasAccountRequiresAHost(t *testing.T) {
	_, err := (Client{}).HasAccount(context.Background(), "0241234567", "")
	if err == nil {
		t.Fatal("expected an error when Mithril is not configured")
	}
}
