package httpapi

import (
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

	"pay2own.com/backend/internal/paystack"
	"pay2own.com/backend/internal/store"
)

type Installments interface {
	NextInstallment(ctx context.Context, imei string) (store.NextInstallment, error)
	DeviceIMEIByToken(ctx context.Context, token string) (string, error)
	StartPayment(ctx context.Context, imei string) (store.PendingPayment, error)
	FailPayment(ctx context.Context, reference string) error
	CompletePayment(ctx context.Context, reference string, amountMinor int64, currency string) error
}

// Charger is the part of the Paystack client the API needs.
type Charger interface {
	Charge(ctx context.Context, req paystack.ChargeRequest) (string, error)
	Verify(ctx context.Context, reference string) (paystack.Verification, error)
	ValidSignature(body []byte, signature string) bool
}

type Options struct {
	Charger Charger // nil means payments are switched off
}

var imeiPattern = regexp.MustCompile(`^[0-9]{15}$`)

func NewRouter(inst Installments, log *slog.Logger, opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /api/devices/{imei}/next-installment",
		requireDevice(inst, log, installmentHandler(inst.NextInstallment, log)))
	if opts.Charger != nil {
		mux.HandleFunc("POST /api/devices/{imei}/payments",
			requireDevice(inst, log, startPaymentHandler(inst, opts.Charger, log)))
		mux.HandleFunc("POST /webhooks/paystack", paystackWebhook(inst, opts.Charger, log))
	}
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
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		p, err := inst.StartPayment(ctx, r.PathValue("imei"))
		switch {
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
			if ferr := inst.FailPayment(context.WithoutCancel(ctx), p.Reference); ferr != nil {
				log.Error("fail payment", "reference", p.Reference, "err", ferr)
			}
			http.Error(w, "Payment provider unavailable", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"reference": p.Reference, "status": status})
	}
}

func paystackWebhook(inst Installments, ch Charger, log *slog.Logger) http.HandlerFunc {
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

		err = inst.CompletePayment(ctx, ref, v.AmountMinor, v.Currency)
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
			log.Info("payment completed", "reference", ref)
		}
		w.WriteHeader(http.StatusOK)
	}
}
