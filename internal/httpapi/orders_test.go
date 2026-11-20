package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pay2own.com/backend/internal/store"
)

type memoryOrders struct {
	saved []store.OrderSlip
	out   bool
}

func (m *memoryOrders) InsertOrder(_ context.Context, order store.OrderSlip) (store.OrderSlip, error) {
	if m.out {
		return store.OrderSlip{}, store.ErrOutOfStock
	}
	order.Reference = "P2O-TEST1"
	order.Status = "open"
	order.IMEI = "356938035643809"
	m.saved = append(m.saved, order)
	return order, nil
}

func (m *memoryOrders) SetOrderChannels(_ context.Context, reference, workerChannel, supportChannel string) error {
	m.saved[0].Reference = reference
	m.saved[0].WorkerChannel = workerChannel
	m.saved[0].SupportChannel = supportChannel
	return nil
}

func (m *memoryOrders) ListOrders(context.Context) ([]store.OrderSlip, error) {
	return m.saved, nil
}

func (m *memoryOrders) CancelOrder(_ context.Context, reference string) (store.OrderSlip, error) {
	for i, order := range m.saved {
		if order.Reference == reference {
			m.saved[i].Status = "cancelled"
			return m.saved[i], nil
		}
	}
	return store.OrderSlip{}, store.ErrNotFound
}

type lookupResult struct {
	account bool
	err     error
	phone   string
}

func (l *lookupResult) HasAccount(_ context.Context, phone, _ string) (bool, error) {
	l.phone = phone
	return l.account, l.err
}

type sentMessage struct {
	phone string
	text  string
}

type memoryMessenger struct {
	sent []sentMessage
}

func (m *memoryMessenger) DeliverText(_ context.Context, phone, text string, _ map[string]string) (string, error) {
	m.sent = append(m.sent, sentMessage{phone: phone, text: text})
	if strings.Contains(phone, "000000000") {
		return "sms", nil
	}
	return "whatsapp", nil
}

func TestOrderSlipChecksMithrilThenStoresLocally(t *testing.T) {
	orders := &memoryOrders{}
	lookup := &lookupResult{account: true}
	messages := &memoryMessenger{}
	handler := OrderService{
		Store:        orders,
		Lookup:       lookup,
		Notify:       messages,
		Token:        "pay2own-local-dev",
		SupportPhone: "+233544415766",
	}.create(slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"name":"Ama Mensah","phone":"0241234567","workerId":"IC-12","catalogPhoneId":"row_testphone000001","phoneModel":"Samsung Galaxy A06","visit":"As soon as possible"}`
	req := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(body))
	req.Header.Set("x-pay2own-token", "pay2own-local-dev")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if lookup.phone != "0241234567" {
		t.Fatalf("lookup phone %s", lookup.phone)
	}
	if orders.saved[0].MithrilAccount == nil || !*orders.saved[0].MithrilAccount {
		t.Fatal("expected the mithril account flag to be stored")
	}
	if orders.saved[0].WorkerChannel != "whatsapp" || orders.saved[0].SupportChannel != "whatsapp" {
		t.Fatalf("channels %#v", orders.saved[0])
	}
	if len(messages.sent) != 2 {
		t.Fatalf("messages %#v", messages.sent)
	}
}

func TestOrderSlipDoesNotCallMithrilForStorage(t *testing.T) {
	orders := &memoryOrders{}
	lookup := &lookupResult{account: false}
	handler := OrderService{
		Store:  orders,
		Lookup: lookup,
		Notify: &memoryMessenger{},
		Token:  "secret",
	}.list(slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	req.Header.Set("x-pay2own-token", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}
