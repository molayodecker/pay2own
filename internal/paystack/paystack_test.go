package paystack

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakePaystack(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("sk_test_fake")
	c.baseURL = srv.URL
	return c
}

func TestChargeSendsRightRequest(t *testing.T) {
	c := fakePaystack(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/charge" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_test_fake" {
			t.Errorf("missing secret key header")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["amount"] != "30000" || body["currency"] != "GHS" || body["reference"] != "p2o_test" {
			t.Errorf("bad body: %v", body)
		}
		w.Write([]byte(`{"status":true,"message":"Charge attempted","data":{"status":"pay_offline"}}`))
	})

	status, err := c.Charge(context.Background(), ChargeRequest{
		Reference: "p2o_test", Email: "customer@example.com",
		AmountMinor: 30000, Currency: "GHS", Phone: "0551234987", Provider: "mtn",
	})
	if err != nil || status != "pay_offline" {
		t.Fatalf("got %q, %v", status, err)
	}
}

func TestChargeRefused(t *testing.T) {
	c := fakePaystack(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":false,"message":"Invalid phone number"}`))
	})
	if _, err := c.Charge(context.Background(), ChargeRequest{Reference: "x"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestVerify(t *testing.T) {
	c := fakePaystack(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transaction/verify/p2o_test" {
			t.Errorf("got path %s", r.URL.Path)
		}
		w.Write([]byte(`{"status":true,"message":"ok","data":{"status":"success","amount":30000,"currency":"GHS"}}`))
	})
	v, err := c.Verify(context.Background(), "p2o_test")
	if err != nil || v.Status != "success" || v.AmountMinor != 30000 || v.Currency != "GHS" {
		t.Fatalf("got %+v, %v", v, err)
	}
}

func TestServerErrorIsAnError(t *testing.T) {
	c := fakePaystack(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.Verify(context.Background(), "p2o_test"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidSignature(t *testing.T) {
	c := New("sk_test_fake")
	body := []byte(`{"event":"charge.success"}`)
	mac := hmac.New(sha512.New, []byte("sk_test_fake"))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	if !c.ValidSignature(body, good) {
		t.Fatal("good signature rejected")
	}
	if c.ValidSignature(body, "bad") || c.ValidSignature([]byte(`{"event":"tampered"}`), good) {
		t.Fatal("bad signature accepted")
	}
}
