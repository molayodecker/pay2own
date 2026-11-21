package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"pay2own.com/backend/internal/store"
)

// CatalogBook is the website phone list in the Pay2Own database.
type CatalogBook interface {
	ListCatalog(ctx context.Context) ([]store.CatalogPhone, error)
	GetCatalog(ctx context.Context, id string) (store.CatalogPhone, error)
	SaveCatalog(ctx context.Context, phone store.CatalogPhone) (store.CatalogPhone, error)
	DeleteCatalog(ctx context.Context, id string) error
}

// CatalogService serves the phones shown on the site.
type CatalogService struct {
	Store CatalogBook
	Token string
}

type catalogJSON struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Short         string   `json:"short"`
	Description   string   `json:"description"`
	Image         string   `json:"image"`
	Price         *float64 `json:"price"`
	InStock       bool     `json:"inStock"`
	Available     int      `json:"available"`
	IMEIs         []string `json:"imeis,omitempty"`
	ReservedIMEIs []string `json:"reservedImeis,omitempty"`
}

func (s CatalogService) list(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		phones, err := s.Store.ListCatalog(ctx)
		if err != nil {
			log.Error("catalog list failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The phone list is unavailable."})
			return
		}
		out := make([]catalogJSON, 0, len(phones))
		for _, phone := range phones {
			out = append(out, toCatalogJSON(phone))
		}
		writeJSON(w, log, http.StatusOK, map[string]any{"phones": out})
	}
}

func (s CatalogService) create(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		phone, ok := readCatalog(w, r, log, "")
		if !ok {
			return
		}
		if phone.ID == "" {
			phone.ID = newCatalogID()
		}
		s.writePhone(w, r, log, phone, http.StatusCreated)
	}
}

func (s CatalogService) get(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		phone, err := s.Store.GetCatalog(ctx, r.PathValue("id"))
		if err == store.ErrNotFound {
			writeJSON(w, log, http.StatusNotFound, map[string]string{"error": "That phone is not on the list."})
			return
		}
		if err != nil {
			log.Error("catalog get failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The phone list is unavailable."})
			return
		}
		writeJSON(w, log, http.StatusOK, toCatalogJSON(phone))
	}
}

func (s CatalogService) save(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		phone, ok := readCatalog(w, r, log, r.PathValue("id"))
		if !ok {
			return
		}
		s.writePhone(w, r, log, phone, http.StatusOK)
	}
}

func (s CatalogService) remove(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, log, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := s.Store.DeleteCatalog(ctx, r.PathValue("id"))
		if err == store.ErrNotFound {
			writeJSON(w, log, http.StatusNotFound, map[string]string{"error": "That phone is not on the list."})
			return
		}
		if err == store.ErrCatalogReserved {
			writeJSON(w, log, http.StatusConflict, map[string]string{"error": "Cancel open orders before deleting this phone."})
			return
		}
		if err != nil {
			log.Error("catalog delete failed", "err", err)
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The phone list is unavailable."})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s CatalogService) writePhone(w http.ResponseWriter, r *http.Request, log *slog.Logger, phone store.CatalogPhone, status int) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	saved, err := s.Store.SaveCatalog(ctx, phone)
	if err == store.ErrBadIMEI {
		writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "Each IMEI is 15 digits."})
		return
	}
	if err == store.ErrIMEITaken {
		writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "That IMEI is already on a phone."})
		return
	}
	if err != nil {
		log.Error("catalog save failed", "err", err)
		writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"error": "The phone list is unavailable."})
		return
	}
	writeJSON(w, log, status, toCatalogJSON(saved))
}

func (s CatalogService) authorized(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got := r.Header.Get("x-pay2own-token")
	if len(got) != len(s.Token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func readCatalog(w http.ResponseWriter, r *http.Request, log *slog.Logger, id string) (store.CatalogPhone, bool) {
	var body catalogJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2_000_000)).Decode(&body); err != nil {
		writeJSON(w, log, http.StatusBadRequest, map[string]string{"error": "Check the phone and try again."})
		return store.CatalogPhone{}, false
	}
	if id != "" {
		body.ID = id
	}
	phone := store.CatalogPhone{
		ID:          strings.TrimSpace(body.ID),
		Name:        clip(body.Name, 120),
		Short:       clip(body.Short, 40),
		Description: clip(body.Description, 500),
		Image:       strings.TrimSpace(body.Image),
		IMEIs:       body.IMEIs,
	}
	if phone.Image == "" {
		phone.Image = "/assets/icon.svg"
	}
	if phone.Name == "" || phone.Short == "" || len(phone.Image) > 1_500_000 {
		writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "Enter the phone name and a short name. Use a smaller photo if you added one."})
		return store.CatalogPhone{}, false
	}
	if body.Price != nil {
		pesewas := int64(math.Round(*body.Price * 100))
		if pesewas < 0 {
			writeJSON(w, log, http.StatusUnprocessableEntity, map[string]string{"error": "Enter a price in cedis, or leave it blank."})
			return store.CatalogPhone{}, false
		}
		phone.PriceMinor = sql.NullInt64{Int64: pesewas, Valid: true}
	}
	return phone, true
}

func toCatalogJSON(phone store.CatalogPhone) catalogJSON {
	out := catalogJSON{
		ID:          phone.ID,
		Name:        phone.Name,
		Short:       phone.Short,
		Description: phone.Description,
		Image:       phone.Image,
		Available:   phone.Available,
		InStock:     phone.Available > 0,
		IMEIs:       phone.IMEIs,
	}
	for _, unit := range phone.Units {
		if unit.Status == "reserved" {
			out.ReservedIMEIs = append(out.ReservedIMEIs, unit.IMEI)
		}
	}
	if phone.PriceMinor.Valid {
		major := float64(phone.PriceMinor.Int64) / 100
		out.Price = &major
	}
	return out
}

func newCatalogID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "row_localcatalog"
	}
	return "row_" + hex.EncodeToString(buf)
}
