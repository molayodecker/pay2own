package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

func newRouter(
	next func(context.Context, string) (NextInstallment, error),
	payNext func(context.Context, string) (NextInstallment, error),
) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("GET /api/devices/{imei}/next-installment", nextInstallmentHandler(next))
	mux.HandleFunc("POST /api/devices/{imei}/next-installment/pay", nextInstallmentHandler(payNext))
	return mux
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := openDatabase(dsn)
	if err != nil {
		log.Fatal("Could not connect to PostgreSQL")
	}
	defer db.Close()

	log.Println("PostgreSQL connection verified")
	router := newRouter(
		func(ctx context.Context, imei string) (NextInstallment, error) {
			return findNextInstallment(ctx, db, imei)
		},
		func(ctx context.Context, imei string) (NextInstallment, error) {
			return payNextInstallment(ctx, db, imei)
		},
	)

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("Starting Pay2Own on port 8080")
	log.Fatal(server.ListenAndServe())
}
