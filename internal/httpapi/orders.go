package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pay2own.com/backend/internal/notify"
	"pay2own.com/backend/internal/store"
)

const (
	orderLookupTimeout = 5 * time.Second
	orderInsertTimeout = 5 * time.Second
	orderRecordTimeout = 3 * time.Second
	// Worker and support each try WhatsApp, then SMS.
	orderDeliveries = 4
)

// OrderBook is the Pay2Own database. Mithril is not this store.
type OrderBook interface {
	InsertOrder(ctx context.Context, order store.OrderSlip) (store.OrderSlip, error)
	SetOrderChannels(ctx context.Context, reference, workerChannel, supportChannel string) error
	ListOrders(ctx context.Context) ([]store.OrderSlip, error)
	CancelOrder(ctx context.Context, reference string) (store.OrderSlip, error)
}

// WorkerLookup asks Mithril whether the worker already has an account.
type WorkerLookup interface {
	HasAccount(ctx context.Context, phone, workerID string) (bool, error)
}

// OrderMessenger sends the slip by WhatsApp, then SMS.
type OrderMessenger interface {
	DeliverText(ctx context.Context, phone, text string, vars map[string]string) (string, error)
}

// OrderService is the website's order desk.
type OrderService struct {
	Store        OrderBook
	Lookup       WorkerLookup
	Notify       OrderMessenger
	Token        string
	SupportPhone string
}

func (s OrderService) create(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var body struct {
			Name           string `json:"name"`
			Phone          string `json:"phone"`
			WorkerID       string `json:"workerId"`
			CatalogPhoneID string `json:"catalogPhoneId"`
			PhoneModel     string `json:"phoneModel"`
			Visit          string `json:"visit"`
			CompanyWebsite string `json:"companyWebsite"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, log, http.StatusBadRequest, map[string]string{"error": "Check the order slip and try again."})
			return
		}
		if strings.TrimSpace(body.CompanyWebsite) != "" {
			writeJSON(w, log, http.StatusCreated, store.OrderSlip{Reference: "P2O-LOCAL", Name: strings.TrimSpace(body.Name), Phone: strings.TrimSpace(body.Phone), Visit: body.Visit, PhoneModel: body.PhoneModel})
			return
		}
		order := store.OrderSlip{
			Name:           clip(body.Name, 120),
			Phone:          clip(body.Phone, 20),
			WorkerID:       clip(body.WorkerID, 40),
			CatalogPhoneID: strings.TrimSpace(body.CatalogPhoneID),
			PhoneModel:     clip(body.PhoneModel, 120),
			Visit:          strings.TrimSpace(body.Visit),
		}
		if order.Name == "" || order.Phone == "" || order.PhoneModel == "" || order.CatalogPhoneID == "" || !allowedVisit(order.Visit) {
			writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "Check the order slip and try again."})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), orderLookupTimeout+orderInsertTimeout+orderDeliveries*notify.DeliveryAttemptTimeout)
		defer cancel()

		if s.Lookup != nil {
			lookupCtx, lookupCancel := context.WithTimeout(ctx, orderLookupTimeout)
			account, err := s.Lookup.HasAccount(lookupCtx, order.Phone, order.WorkerID)
			lookupCancel()
			if err != nil {
				log.Warn("mithril worker lookup failed", "err", err)
			} else {
				order.MithrilAccount = &account
			}
		}

		insertCtx, insertCancel := context.WithTimeout(ctx, orderInsertTimeout)
		saved, err := s.Store.InsertOrder(insertCtx, order)
		insertCancel()
		if err == store.ErrOutOfStock {
			writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "That phone is out of stock. Choose another phone."})
			return
		}
		if err != nil {
			log.Error("order insert failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The order queue is unavailable."})
			return
		}

		workerChannel, supportChannel := s.notify(ctx, saved)
		// Delivery attempts can use the whole order budget. Recording still
		// gets its own deadline after the reservation is committed.
		recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), orderRecordTimeout)
		defer recordCancel()
		if err := s.Store.SetOrderChannels(recordCtx, saved.Reference, workerChannel, supportChannel); err != nil {
			log.Error("order channel update failed", "err", err)
		}
		saved.WorkerChannel = workerChannel
		saved.SupportChannel = supportChannel
		writeJSON(w, log, http.StatusCreated, saved)
	}
}

func (s OrderService) list(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		orders, err := s.Store.ListOrders(ctx)
		if err != nil {
			log.Error("order list failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The order queue is unavailable."})
			return
		}
		writeJSON(w, log, http.StatusOK, map[string]any{"orders": orders})
	}
}

func (s OrderService) cancel(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		order, err := s.Store.CancelOrder(ctx, r.PathValue("reference"))
		if err == store.ErrNotFound {
			writeJSON(w, log, http.StatusNotFound, map[string]string{"error": "That order is not in the queue."})
			return
		}
		if err != nil {
			log.Error("order cancel failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The order queue is unavailable."})
			return
		}
		writeJSON(w, log, http.StatusOK, order)
	}
}

func (s OrderService) notify(ctx context.Context, order store.OrderSlip) (string, string) {
	if s.Notify == nil {
		return "failed", "failed"
	}
	workerText, workerVars := workerMessage(order)
	workerChannel, _ := s.Notify.DeliverText(ctx, order.Phone, workerText, workerVars)
	if workerChannel == "" {
		workerChannel = "failed"
	}
	support := strings.TrimSpace(s.SupportPhone)
	if support == "" || samePhone(order.Phone, support) {
		return workerChannel, workerChannel
	}
	supportText, supportVars := supportMessage(order)
	supportChannel, _ := s.Notify.DeliverText(ctx, support, supportText, supportVars)
	if supportChannel == "" {
		supportChannel = "failed"
	}
	return workerChannel, supportChannel
}

func (s OrderService) authorized(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got := r.Header.Get("x-pay2own-token")
	if len(got) != len(s.Token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func workerMessage(order store.OrderSlip) (string, map[string]string) {
	vars := map[string]string{
		"1": firstName(order.Name),
		"2": order.Reference,
		"3": order.PhoneModel,
		"4": "We will call you to set your office visit.",
	}
	return fillTemplate(vars), vars
}

func supportMessage(order store.OrderSlip) (string, map[string]string) {
	id := order.WorkerID
	if id == "" {
		id = "not given"
	}
	account := "not checked"
	if order.MithrilAccount != nil {
		if *order.MithrilAccount {
			account = "yes"
		} else {
			account = "no"
		}
	}
	detail := order.Name + ", " + order.Phone + ". Worker ID " + id + ". Mithril account " + account + ". Visit " + order.Visit + "."
	vars := map[string]string{
		"1": "Instaclean",
		"2": order.Reference,
		"3": order.PhoneModel,
		"4": clip(detail, 200),
	}
	return fillTemplate(vars), vars
}

func fillTemplate(vars map[string]string) string {
	return "Hello " + vars["1"] + ", Pay2Own received order " + vars["2"] + " for a " + vars["3"] + ". " + vars["4"] + " Keep this reference: " + vars["2"] + "."
}

func firstName(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "there"
	}
	return fields[0]
}

func allowedVisit(visit string) bool {
	switch visit {
	case "As soon as possible", "Later this week", "Next week":
		return true
	default:
		return false
	}
}

func samePhone(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func clip(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > max {
		return value[:max]
	}
	return value
}
