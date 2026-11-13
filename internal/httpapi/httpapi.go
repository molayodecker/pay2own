package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"pay2own.com/backend/internal/store"
)

type Installments interface {
	NextInstallment(ctx context.Context, imei string) (store.NextInstallment, error)
	PayNextInstallment(ctx context.Context, imei string) (store.NextInstallment, error)
	DeviceIMEIByToken(ctx context.Context, token string) (string, error)
}

var imeiPattern = regexp.MustCompile(`^[0-9]{15}$`)

func NewRouter(inst Installments, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /api/devices/{imei}/next-installment",
		requireDevice(inst, log, installmentHandler(inst.NextInstallment, log)))
	mux.HandleFunc("POST /api/devices/{imei}/next-installment/pay",
		requireDevice(inst, log, installmentHandler(inst.PayNextInstallment, log)))
	return mux
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