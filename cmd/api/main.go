package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"pay2own.com/backend/internal/admin"
	"pay2own.com/backend/internal/httpapi"
	"pay2own.com/backend/internal/mithril"
	"pay2own.com/backend/internal/notify"
	"pay2own.com/backend/internal/paystack"
	"pay2own.com/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	// ctx is cancelled when you press Ctrl+C or the host sends SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	key := os.Getenv("PAYSTACK_SECRET_KEY")
	if key == "" {
		return errors.New("PAYSTACK_SECRET_KEY is required")
	}

	alerts, err := notify.FromEnv(
		os.Getenv("FIREBASE_SERVICE_ACCOUNT_FILE"),
		os.Getenv("TWILIO_ACCOUNT_SID"),
		os.Getenv("TWILIO_AUTH_TOKEN"),
		os.Getenv("TWILIO_MESSAGING_SERVICE_SID"),
		notify.TwilioFromNumber(os.Getenv("TWILIO_PHONE_NUMBER"), os.Getenv("TWILIO_FROM_NUMBER")),
		os.Getenv("TWILIO_WHATSAPP_FROM"),
		os.Getenv("TWILIO_TEMPLATE_PAY2OWN_ORDER"),
	)
	if err != nil {
		return err
	}

	st := store.New(db)
	appEnv := os.Getenv("APP_ENV")
	lookup := mithril.Client{
		BaseURL: mithril.APIBase(appEnv, os.Getenv("MITHRIL_API_URL")),
		Token:   mithril.IntakeToken(appEnv, os.Getenv("PAY2OWN_INTAKE_TOKEN")),
	}
	supportPhone := cmp.Or(os.Getenv("PAY2OWN_SUPPORT_PHONE"), "+233544415766")
	log.Info("mithril lookup", "base", lookup.BaseURL)
	apiHandler := httpapi.NewRouter(st, log, httpapi.Options{
		Charger: paystack.New(key),
		Notify:  alerts,
		Orders: httpapi.OrderService{
			Store:        st,
			Lookup:       lookup,
			Notify:       messenger(alerts),
			Token:        lookup.Token,
			SupportPhone: supportPhone,
		},
		Catalog: httpapi.CatalogService{
			Store: st,
			Token: lookup.Token,
		},
	})
	shop := admin.New(st, os.Getenv("ADMIN_PASSWORD"), []byte(os.Getenv("ADMIN_SESSION_SECRET")), admin.Provision{
		APKURL:            os.Getenv("PROVISIONING_APK_URL"),
		SignatureChecksum: os.Getenv("PROVISIONING_SIGNATURE_CHECKSUM"),
		PackageChecksum:   os.Getenv("PROVISIONING_APK_CHECKSUM"),
		WifiSSID:          os.Getenv("PROVISIONING_WIFI_SSID"),
		WifiPassword:      os.Getenv("PROVISIONING_WIFI_PASSWORD"),
		WifiSecurity:      os.Getenv("PROVISIONING_WIFI_SECURITY"),
	}, log)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectShopRoot(w, r) {
			return
		}
		if r.URL.Path == "/pay2own.apk" {
			serveAPK(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/admin") {
			shop.ServeHTTP(w, r)
			return
		}
		apiHandler.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// redirectShopRoot sends the android shop hostname to the admin home.
func redirectShopRoot(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	if !strings.EqualFold(host, "android.pay2own.app") || r.URL.Path != "/" {
		return false
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
	return true
}

func messenger(alerts notify.Sender) httpapi.OrderMessenger {
	hub, ok := alerts.(notify.Hub)
	if !ok {
		return nil
	}
	return hub
}

func serveAPK(w http.ResponseWriter, r *http.Request) {
	path := os.Getenv("APK_PATH")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	http.ServeFile(w, r, path)
}
