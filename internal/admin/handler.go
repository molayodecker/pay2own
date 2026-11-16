package admin

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"sync"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"pay2own.com/backend/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

// Shop is the store the shop screens need.
type Shop interface {
	CreateSale(ctx context.Context, sale store.Sale) (int64, error)
	ActivateDevice(ctx context.Context, imei string) error
	IssueDeviceToken(ctx context.Context, imei string) (string, error)
	DeviceForAdmin(ctx context.Context, imei string) (store.AdminDevice, error)
}

// Handler serves the staff pages under /admin.
type Handler struct {
	shop      Shop
	password  string
	secret    []byte
	provision Provision
	log       *slog.Logger
	tmpl      *template.Template
	mux       *http.ServeMux
	loginMu   sync.Mutex
	failures  map[string]loginFailures
}

func New(shop Shop, password string, secret []byte, provision Provision, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &Handler{
		shop: shop, password: password, secret: secret, provision: provision, log: log,
		tmpl: template.Must(template.ParseFS(templateFiles, "templates/*.html")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", h.loginForm)
	mux.HandleFunc("POST /admin/login", h.login)
	mux.HandleFunc("POST /admin/logout", h.logout)
	mux.HandleFunc("GET /admin", h.home)
	mux.HandleFunc("GET /admin/find", h.find)
	mux.HandleFunc("GET /admin/model", h.model)
	mux.HandleFunc("POST /admin/sales", h.createSale)
	mux.HandleFunc("GET /admin/devices/{imei}", h.device)
	mux.HandleFunc("POST /admin/devices/{imei}/token", h.reissue)
	mux.HandleFunc("POST /admin/devices/{imei}/activate", h.activate)
	h.mux = mux
	return h.logged(mux)
}

func (h *Handler) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		_, pattern := h.mux.Handler(r)
		if pattern == "" {
			pattern = "unmatched"
		}
		h.log.Info("request", "method", r.Method, "route", pattern, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (h *Handler) mustStaff(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !h.configured() {
		http.Error(w, "Shop admin is not configured. Set ADMIN_PASSWORD and ADMIN_SESSION_SECRET.", http.StatusServiceUnavailable)
		return "", false
	}
	csrf, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return "", false
	}
	return csrf, true
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		http.Error(w, "Shop admin is not configured. Set ADMIN_PASSWORD and ADMIN_SESSION_SECRET.", http.StatusServiceUnavailable)
		return
	}
	if _, ok := h.session(r); ok {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	h.render(w, http.StatusOK, "login", loginView{})
}

// loginFailures bounds repeated guesses of the shared shop password.
type loginFailures struct {
	count int
	until time.Time
}

func (h *Handler) loginKey(r *http.Request) string {
	// Do not trust caller-controlled forwarding headers for rate-limit identity.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil { return r.RemoteAddr }
	return host
}

func (h *Handler) blockedLogin(key string) bool {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	v := h.failures[key]
	if time.Now().After(v.until) {
		delete(h.failures, key)
		return false
	}
	return v.count >= 5
}

func (h *Handler) recordLogin(key string, success bool) {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	if success {
		delete(h.failures, key)
		return
	}
	if h.failures == nil { h.failures = make(map[string]loginFailures) }
	v := h.failures[key]
	if time.Now().After(v.until) { v = loginFailures{until: time.Now().Add(15 * time.Minute)} }
	v.count++
	h.failures[key] = v
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		http.Error(w, "Shop admin is not configured. Set ADMIN_PASSWORD and ADMIN_SESSION_SECRET.", http.StatusServiceUnavailable)
		return
	}
	key := h.loginKey(r)
	if h.blockedLogin(key) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "Too many sign-in attempts. Try again later.", http.StatusTooManyRequests)
		return
	}
	if !sameSecret(r.FormValue("password"), h.password) {
		h.recordLogin(key, false)
		h.render(w, http.StatusUnauthorized, "login", loginView{Error: "Wrong password."})
		return
	}
	h.recordLogin(key, true)
	if _, err := h.startSession(w, r); err != nil {
		http.Error(w, "Could not start the session.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.mustStaff(w, r); !ok {
		return
	}
	if !h.checkCSRF(r) {
		http.Error(w, "Form expired. Sign in and try again.", http.StatusBadRequest)
		return
	}
	h.clearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	h.render(w, http.StatusOK, "sale", saleView{CSRF: csrf, Form: saleForm{Provider: "mtn"}})
}

func (h *Handler) find(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	imei := strings.TrimSpace(r.URL.Query().Get("imei"))
	if !imeiPattern.MatchString(imei) {
		h.render(w, http.StatusBadRequest, "sale", saleView{CSRF: csrf, Error: "IMEI must be 15 digits.", Form: saleForm{Provider: "mtn"}})
		return
	}
	http.Redirect(w, r, "/admin/devices/"+imei, http.StatusSeeOther)
}

func (h *Handler) model(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		http.Error(w, "Shop admin is not configured. Set ADMIN_PASSWORD and ADMIN_SESSION_SECRET.", http.StatusServiceUnavailable)
		return
	}
	if _, ok := h.session(r); !ok {
		http.Error(w, "Sign in again.", http.StatusUnauthorized)
		return
	}
	imei := strings.TrimSpace(r.URL.Query().Get("imei"))
	if !imeiPattern.MatchString(imei) {
		http.Error(w, "IMEI must be 15 digits.", http.StatusBadRequest)
		return
	}
	name, err := modelLookup(r.Context(), imei)
	if err != nil {
		h.log.Info("model lookup", "err", err)
		name = ""
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Model string `json:"model"`
	}{Model: name})
}

func (h *Handler) createSale(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	if !h.checkCSRF(r) {
		http.Error(w, "Form expired. Sign in and try again.", http.StatusBadRequest)
		return
	}
	form := saleForm{
		Name: r.FormValue("name"), Phone: r.FormValue("phone"), Provider: r.FormValue("network"),
		IMEI: r.FormValue("imei"), Model: r.FormValue("model"), Price: r.FormValue("price"),
		Deposit: r.FormValue("deposit"), Weeks: r.FormValue("weeks"), FirstDue: r.FormValue("first-due"),
	}
	sale, message := form.sale()
	if message != "" {
		h.render(w, http.StatusBadRequest, "sale", saleView{CSRF: csrf, Error: message, Form: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := h.shop.CreateSale(ctx, sale); err != nil {
		msg := "Could not save the sale."
		if strings.Contains(err.Error(), "devices_imei_key") {
			msg = "A phone with this IMEI is already registered."
		} else if strings.Contains(err.Error(), "invalid sale") {
			msg = "Check the price, deposit, and number of weeks. The deposit has to be less than the price."
		}
		h.render(w, http.StatusBadRequest, "sale", saleView{CSRF: csrf, Error: msg, Form: form})
		return
	}
	token, err := h.shop.IssueDeviceToken(ctx, sale.IMEI)
	if err != nil {
		http.Error(w, "The sale was saved, but the token was not issued. Open the phone and issue a token.", http.StatusInternalServerError)
		return
	}
	h.renderHandoff(w, csrf, sale, token)
}

func (h *Handler) device(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	imei := r.PathValue("imei")
	if !imeiPattern.MatchString(imei) {
		http.Error(w, "IMEI must be 15 digits.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	device, err := h.shop.DeviceForAdmin(ctx, imei)
	if err != nil {
		h.render(w, http.StatusNotFound, "sale", saleView{CSRF: csrf, Error: "No phone with that IMEI.", Form: saleForm{Provider: "mtn", IMEI: imei}})
		return
	}
	h.render(w, http.StatusOK, "device", deviceView{
		CSRF: csrf, Device: device, Price: cedis(device.PriceMinor), Deposit: cedis(device.DepositMinor),
	})
}

func (h *Handler) reissue(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	if !h.checkCSRF(r) {
		http.Error(w, "Form expired. Sign in and try again.", http.StatusBadRequest)
		return
	}
	imei := r.PathValue("imei")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	device, err := h.shop.DeviceForAdmin(ctx, imei)
	if err != nil {
		http.Error(w, "No phone with that IMEI.", http.StatusNotFound)
		return
	}
	token, err := h.shop.IssueDeviceToken(ctx, imei)
	if err != nil {
		h.render(w, http.StatusInternalServerError, "device", deviceView{
			CSRF: csrf, Device: device, Price: cedis(device.PriceMinor), Deposit: cedis(device.DepositMinor),
			Error: "Could not issue a token.",
		})
		return
	}
	h.renderHandoff(w, csrf, store.Sale{
		FullName: device.CustomerName, Phone: device.Phone, IMEI: device.IMEI, Model: device.Model,
		PriceMinor: device.PriceMinor, DepositMinor: device.DepositMinor,
	}, token)
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	csrf, ok := h.mustStaff(w, r)
	if !ok {
		return
	}
	if !h.checkCSRF(r) {
		http.Error(w, "Form expired. Sign in and try again.", http.StatusBadRequest)
		return
	}
	imei := r.PathValue("imei")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.shop.ActivateDevice(ctx, imei); err != nil {
		device, loadErr := h.shop.DeviceForAdmin(ctx, imei)
		if loadErr != nil {
			http.Error(w, "No phone with that IMEI.", http.StatusNotFound)
			return
		}
		h.render(w, http.StatusConflict, "device", deviceView{
			CSRF: csrf, Device: device, Price: cedis(device.PriceMinor), Deposit: cedis(device.DepositMinor),
			Error: "Restrictions stay as they are. A phone that is unlocking or already released is not locked again.",
		})
		return
	}
	http.Redirect(w, r, "/admin/devices/"+imei, http.StatusSeeOther)
}

func (h *Handler) renderHandoff(w http.ResponseWriter, csrf string, sale store.Sale, token string) {
	enroll, err := qrImage(enrollURL(sale.IMEI, token))
	if err != nil {
		http.Error(w, "Could not draw the enroll code.", http.StatusInternalServerError)
		return
	}
	view := handoffView{
		CSRF: csrf, Name: sale.FullName, Phone: sale.Phone, Model: sale.Model, IMEI: sale.IMEI,
		Price: cedis(sale.PriceMinor), Deposit: cedis(sale.DepositMinor), Token: token,
		EnrollURL: enrollURL(sale.IMEI, token), EnrollQR: enroll,
	}
	if raw, ok := h.provision.payload(); ok {
		img, err := qrImage(raw)
		if err != nil {
			http.Error(w, "Could not draw the device-owner code.", http.StatusInternalServerError)
			return
		}
		view.ProvisionQR = img
	}
	h.render(w, http.StatusOK, "handoff", view)
}

func qrImage(content string) (template.URL, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, 512)
	if err != nil {
		return "", err
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png)), nil
}

type loginView struct {
	Error string
}

type saleView struct {
	CSRF  string
	Error string
	Form  saleForm
}

type deviceView struct {
	CSRF    string
	Error   string
	Device  store.AdminDevice
	Price   string
	Deposit string
}

type handoffView struct {
	CSRF        string
	Name        string
	Phone       string
	Model       string
	IMEI        string
	Price       string
	Deposit     string
	Token       string
	EnrollURL   string
	EnrollQR    template.URL
	ProvisionQR template.URL
}

func (h *Handler) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		h.log.Error("render", "template", name, "err", err)
	}
}
