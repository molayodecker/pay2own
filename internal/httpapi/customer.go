package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pay2own.com/backend/internal/store"
	"pay2own.com/backend/internal/notify"
)

func loanHandler(inst Installments, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		sum, err := inst.LoanSummary(ctx, r.PathValue("imei"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "No loan", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("loan summary", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, log, http.StatusOK, sum)
	}
}

func paymentsHandler(inst Installments, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		list, err := inst.ListPayments(ctx, r.PathValue("imei"))
		if err != nil {
			log.Error("list payments", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if list == nil {
			list = []store.PaymentRecord{}
		}
		writeJSON(w, log, http.StatusOK, map[string]any{"payments": list})
	}
}

func paymentHandler(inst Installments, ch Charger, alerts notify.Sender, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		imei := r.PathValue("imei")
		ref := r.PathValue("reference")
		rec, err := inst.Payment(ctx, imei, ref)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("payment", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if rec.Status == "pending" && ch != nil {
			if v, verr := ch.Verify(ctx, rec.Reference); verr != nil {
				log.Error("verify payment", "reference", rec.Reference, "err", verr)
			} else {
				switch v.Status {
				case "success":
					fresh, err := inst.CompletePayment(ctx, rec.Reference, v.AmountMinor, v.Currency)
					if err != nil {
						log.Error("complete payment", "reference", rec.Reference, "err", err)
					} else if fresh {
						notifyPayment(ctx, inst, alerts, log, rec.Reference, v.AmountMinor, v.Currency)
					}
				case "failed", "abandoned", "reversed":
					if err := inst.FailPayment(ctx, rec.Reference); err != nil {
						log.Error("fail payment", "reference", rec.Reference, "err", err)
					}
				}
				if refreshed, rerr := inst.Payment(ctx, imei, ref); rerr == nil {
					rec = refreshed
				}
			}
		}
		writeJSON(w, log, http.StatusOK, rec)
	}
}

func pushTokenHandler(inst Installments, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if len(strings.TrimSpace(in.Token)) < 20 {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := inst.SavePushToken(ctx, r.PathValue("imei"), in.Token)
		switch {
		case errors.Is(err, store.ErrBadRequest):
			http.Error(w, "Bad Request", http.StatusBadRequest)
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, "Not Found", http.StatusNotFound)
		case err != nil:
			log.Error("save push token", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error("encode response", "err", err)
	}
}
