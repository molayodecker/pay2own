package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"pay2own.com/backend/internal/notify"
	"pay2own.com/backend/internal/paystack"
	"pay2own.com/backend/internal/store"
)

type Installments interface {
	NextInstallment(ctx context.Context, imei string) (store.NextInstallment, error)
	DeviceIMEIByToken(ctx context.Context, token string) (string, error)
	StartPayment(ctx context.Context, imei string, installments int) (store.PendingPayment, error)
	FailPayment(ctx context.Context, reference string) error
	CompletePayment(ctx context.Context, reference string, amountMinor int64, currency string) (bool, error)
	LoanSummary(ctx context.Context, imei string) (store.LoanSummary, error)
	ListPayments(ctx context.Context, imei string) ([]store.PaymentRecord, error)
	Payment(ctx context.Context, imei, reference string) (store.PaymentRecord, error)
	SavePushToken(ctx context.Context, imei, token string) error
	DeviceRestrictionState(ctx context.Context, imei string) (string, error)
	ConfirmRelease(ctx context.Context, imei string) error
}

// paymentContacts is optional. The store implements it; tests that don't send alerts don't.
type paymentContacts interface {
	PaymentContact(ctx context.Context, reference string) (store.PaymentContact, error)
}

// Charger is the part of the Paystack client the API needs.
type Charger interface {
	Charge(ctx context.Context, req paystack.ChargeRequest) (string, error)
	Verify(ctx context.Context, reference string) (paystack.Verification, error)
	ValidSignature(body []byte, signature string) bool
}

type Options struct {
	Charger Charger       // nil means payments are switched off
	Notify  notify.Sender // nil means nobody is told when a payment lands
}

var imeiPattern = regexp.MustCompile(`^[0-9]{15}$`)

func NewRouter(inst Installments, log *slog.Logger, opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /api/devices/{imei}/next-installment",
		requireDevice(inst, log, installmentHandler(inst.NextInstallment, log)))
	mux.HandleFunc("GET /api/devices/{imei}/loan",
		requireDevice(inst, log, loanHandler(inst, log)))
	mux.HandleFunc("GET /api/devices/{imei}/payments",
		requireDevice(inst, log, paymentsHandler(inst, log)))
	mux.HandleFunc("GET /api/devices/{imei}/payments/{reference}",
		requireDevice(inst, log, paymentHandler(inst, opts.Charger, opts.Notify, log)))
	mux.HandleFunc("POST /api/devices/{imei}/push-token",
		requireDevice(inst, log, pushTokenHandler(inst, log)))
	if opts.Charger != nil {
		mux.HandleFunc("POST /api/devices/{imei}/payments",
			requireDevice(inst, log, startPaymentHandler(inst, opts.Charger, log)))
		mux.HandleFunc("POST /webhooks/paystack", paystackWebhook(inst, opts.Charger, opts.Notify, log))
	} else {
		mux.HandleFunc("POST /api/devices/{imei}/payments", http.NotFound)
	}
	mux.HandleFunc("GET /api/devices/{imei}/restriction",
		requireDevice(inst, log, restrictionHandler(inst, log)))
	mux.HandleFunc("POST /api/devices/{imei}/release-confirmed",
		requireDevice(inst, log, releaseConfirmedHandler(inst, log)))
	return logRequests(log, mux, mux)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("Pay2Own backend is running\n"))
}

func installmentHandler(
	action func(context.Context, string) (store.NextInstallment, error),
	log *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		imei := r.PathValue("imei")
		if !imeiPattern.MatchString(imei) {
			http.Error(w, "Invalid IMEI", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		result, err := action(ctx, imei)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "No installment due", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("installment request failed", "path", r.Pattern, "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Error("encode response", "err", err)
		}
	}
}

// requireDevice only lets a request through if it carries a valid device
// token AND that token belongs to the IMEI in the URL.
func requireDevice(inst Installments, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		imei, err := inst.DeviceIMEIByToken(ctx, token)
		if errors.Is(err, store.ErrNotFound) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			log.Error("token lookup failed", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if imei != r.PathValue("imei") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func startPaymentHandler(inst Installments, ch Charger, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, ok := paymentCount(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		p, err := inst.StartPayment(ctx, r.PathValue("imei"), count)
		switch {
		case errors.Is(err, store.ErrBadRequest):
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, "No installment due", http.StatusNotFound)
			return
		case errors.Is(err, store.ErrAlreadyPending):
			http.Error(w, "A payment is already waiting for approval", http.StatusConflict)
			return
		case err != nil:
			log.Error("start payment", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		status, err := ch.Charge(ctx, paystack.ChargeRequest{
			Reference:   p.Reference,
			Email:       fmt.Sprintf("customer-%d@pay2own.com", p.CustomerID),
			AmountMinor: p.AmountMinor,
			Currency:    p.Currency,
			Phone:       p.Phone,
			Provider:    p.Provider,
		})
		if err != nil {
			log.Error("paystack charge", "reference", p.Reference, "err", err)
			// Only a definitive provider refusal can safely free this installment.
			// An HTTP timeout may have occurred after Paystack accepted the charge.
			if errors.Is(err, paystack.ErrChargeRejected) {
				if ferr := inst.FailPayment(context.WithoutCancel(ctx), p.Reference); ferr != nil {
					log.Error("fail payment", "reference", p.Reference, "err", ferr)
				}
			}
			http.Error(w, "Payment provider unavailable", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"reference": p.Reference, "status": status})
	}
}

// paymentCount reads an optional JSON body.
// An empty body charges the single installment that is due.
// {"installments": n} charges the next n installments.
// {"pay_balance": true} charges whatever is still unpaid.
func paymentCount(w http.ResponseWriter, r *http.Request) (int, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return 0, false
	}
	var in struct {
		Installments int  `json:"installments"`
		PayBalance   bool `json:"pay_balance"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &in); err != nil || in.Installments < 0 || (in.PayBalance && in.Installments > 0) {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return 0, false
		}
	}
	if in.PayBalance {
		return 0, true
	}
	if in.Installments > 1 {
		return in.Installments, true
	}
	return 1, true
}

func paystackWebhook(inst Installments, ch Charger, alerts notify.Sender, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if !ch.ValidSignature(body, r.Header.Get("X-Paystack-Signature")) {
			log.Warn("paystack webhook bad signature")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var event struct {
			Event string `json:"event"`
			Data  struct {
				Reference string `json:"reference"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &event); err != nil || event.Data.Reference == "" {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if event.Event != "charge.success" {
			w.WriteHeader(http.StatusOK) // we don't act on other events yet
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		ref := event.Data.Reference

		v, err := ch.Verify(ctx, ref)
		if err != nil {
			log.Error("paystack verify", "reference", ref, "err", err)
			http.Error(w, "Try again", http.StatusInternalServerError)
			return
		}
		if v.Status != "success" {
			log.Warn("webhook said success but verify did not", "reference", ref, "verify_status", v.Status)
			w.WriteHeader(http.StatusOK)
			return
		}

		fresh, err := inst.CompletePayment(ctx, ref, v.AmountMinor, v.Currency)
		switch {
		case errors.Is(err, store.ErrNotFound):
			log.Warn("webhook for unknown reference", "reference", ref)
		case errors.Is(err, store.ErrAmountMismatch):
			log.Error("payment amount mismatch", "reference", ref, "amount_minor", v.AmountMinor)
		case err != nil:
			log.Error("complete payment", "reference", ref, "err", err)
			http.Error(w, "Try again", http.StatusInternalServerError)
			return
		default:
			log.Info("payment completed", "reference", ref, "fresh", fresh)
			if fresh {
				notifyPayment(ctx, inst, alerts, log, ref, v.AmountMinor, v.Currency)
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

func notifyPayment(ctx context.Context, inst Installments, alerts notify.Sender, log *slog.Logger, reference string, amountMinor int64, currency string) {
	contacts, ok := inst.(paymentContacts)
	if !ok || alerts == nil {
		return
	}
	contact, err := contacts.PaymentContact(ctx, reference)
	if err != nil {
		log.Error("payment contact", "reference", reference, "err", err)
		return
	}
	title := "Payment received"
	body := fmt.Sprintf("We received your %s %d.%02d payment.", currency, amountMinor/100, amountMinor%100)
	if _, err := notify.Deliver(ctx, alerts, contact.PushToken, contact.Phone, title, body); err != nil {
		log.Error("payment alert", "reference", reference, "err", err)
	}
}

func restrictionHandler(inst Installments, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		state, err := inst.DeviceRestrictionState(ctx, r.PathValue("imei"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("restriction state", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"state": state})
	}
}

func releaseConfirmedHandler(inst Installments, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		err := inst.ConfirmRelease(ctx, r.PathValue("imei"))
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, "Not Found", http.StatusNotFound)
		case errors.Is(err, store.ErrNotReleasable):
			http.Error(w, "Device is not pending release", http.StatusConflict)
		case err != nil:
			log.Error("confirm release", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}