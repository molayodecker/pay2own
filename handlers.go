package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"
)

var imeiPattern = regexp.MustCompile(`^[0-9]{15}$`)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "Pay2Own backend is running")
}

func nextInstallmentHandler(find func(context.Context, string) (NextInstallment, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		imei := r.PathValue("imei")
		if !imeiPattern.MatchString(imei) {
			http.Error(w, "Invalid IMEI", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		next, err := find(ctx, imei)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "No installment due", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Printf("find next installment: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(next); err != nil {
			log.Printf("encode next installment: %v", err)
		}
	}
}
