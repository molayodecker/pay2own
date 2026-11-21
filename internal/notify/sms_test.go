package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewTwilioDisabledWithoutCredentials(t *testing.T) {
	if NewTwilio("", "", "", "") != nil {
		t.Fatal("empty config should disable SMS")
	}
	if NewTwilio("ACtest", "token", "", "") != nil {
		t.Fatal("account without a sender should disable SMS")
	}
}

func TestSendSMSTwilio(t *testing.T) {
	var gotMethod, gotUser, gotPass, gotType string
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUser, gotPass, _ = r.BasicAuth()
		gotType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sms := NewTwilio("ACtest", "secret", "MGservice", "+233200000000")
	sms.messagesURL = srv.URL
	if err := sms.SendSMS(context.Background(), "0551234987", "Payment received"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotUser != "ACtest" || gotPass != "secret" {
		t.Fatalf("method=%s user=%s pass=%s", gotMethod, gotUser, gotPass)
	}
	if gotType != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type %s", gotType)
	}
	if form.Get("To") != "+233551234987" || form.Get("Body") != "Payment received" || form.Get("MessagingServiceSid") != "MGservice" || form.Get("From") != "" {
		t.Fatalf("form %+v", form)
	}
}

func TestSendSMSUsesFromNumber(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sms := NewTwilio("ACtest", "secret", "", "+233200000099")
	sms.messagesURL = srv.URL
	if err := sms.SendSMS(context.Background(), "+233551234987", "Hello"); err != nil {
		t.Fatal(err)
	}
	if form.Get("From") != "+233200000099" || form.Get("To") != "+233551234987" || form.Get("MessagingServiceSid") != "" {
		t.Fatalf("form %+v", form)
	}
}

func TestSendSMSStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	sms := NewTwilio("ACtest", "secret", "MGservice", "")
	sms.messagesURL = srv.URL
	if err := sms.SendSMS(context.Background(), "0551234987", "Hi"); err == nil || err.Error() != "sms status 401" {
		t.Fatalf("err = %v", err)
	}
}

func TestDeliverTextFallsBackWhenWhatsAppStalls(t *testing.T) {
	var whatsapp, sms int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		if strings.HasPrefix(form.Get("To"), "whatsapp:") {
			whatsapp++
			<-r.Context().Done()
			return
		}
		sms++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sender := NewTwilio("ACtest", "secret", "MGservice", "+233200000000")
	sender.messagesURL = srv.URL
	sender.WhatsAppFrom = "+233200000000"
	sender.attemptTimeout = 40 * time.Millisecond
	sender.client.Timeout = time.Second

	parent, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	channel, err := sender.DeliverText(parent, "0551234987", "Hello", nil)
	if err != nil || channel != "sms" || whatsapp != 1 || sms != 1 {
		t.Fatalf("channel=%s err=%v whatsapp=%d sms=%d", channel, err, whatsapp, sms)
	}
	if parent.Err() != nil {
		t.Fatal("whatsapp stall consumed the parent deadline")
	}
}

func TestE164(t *testing.T) {
	cases := map[string]string{
		"0551234987":    "+233551234987",
		"233551234987":  "+233551234987",
		"+233551234987": "+233551234987",
		"055 123 4987":  "+233551234987",
	}
	for in, want := range cases {
		got, err := e164(in)
		if err != nil || got != want {
			t.Fatalf("%s -> %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := e164("123"); err == nil {
		t.Fatal("short number should fail")
	}
}
