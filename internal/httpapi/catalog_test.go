package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pay2own.com/backend/internal/store"
)

type memoryCatalog struct {
	phones []store.CatalogPhone
}

func (m *memoryCatalog) ListCatalog(context.Context) ([]store.CatalogPhone, error) {
	return m.phones, nil
}

func (m *memoryCatalog) SaveCatalog(_ context.Context, phone store.CatalogPhone) (store.CatalogPhone, error) {
	imeis, err := normalizeTestIMEIs(phone.IMEIs)
	if err != nil {
		return store.CatalogPhone{}, err
	}
	phone.IMEIs = imeis
	phone.Available = len(imeis)
	phone.InStock = phone.Available > 0
	for i, item := range m.phones {
		if item.ID == phone.ID {
			phone.SortOrder = item.SortOrder
			m.phones[i] = phone
			return phone, nil
		}
	}
	phone.SortOrder = len(m.phones) + 1
	m.phones = append(m.phones, phone)
	return phone, nil
}

func (m *memoryCatalog) GetCatalog(_ context.Context, id string) (store.CatalogPhone, error) {
	for _, item := range m.phones {
		if item.ID == id {
			return item, nil
		}
	}
	return store.CatalogPhone{}, store.ErrNotFound
}

func normalizeTestIMEIs(values []string) ([]string, error) {
	out := []string{}
	for _, value := range values {
		digits := ""
		for _, r := range value {
			if r >= '0' && r <= '9' {
				digits += string(r)
			}
		}
		if digits == "" {
			continue
		}
		if len(digits) != 15 {
			return nil, store.ErrBadIMEI
		}
		out = append(out, digits)
	}
	return out, nil
}

func (m *memoryCatalog) DeleteCatalog(_ context.Context, id string) error {
	for i, item := range m.phones {
		if item.ID == id {
			m.phones = append(m.phones[:i], m.phones[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func TestCatalogPriceIsStoredInPesewasAndStockCanChange(t *testing.T) {
	book := &memoryCatalog{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := CatalogService{Store: book, Token: "pay2own-local-dev"}

	create := httptest.NewRequest(http.MethodPost, "/api/catalog/phones", strings.NewReader(`{"id":"row_testphone000001","name":"Test Phone","short":"Test","description":"A test handset.","image":"/assets/phones/galaxy-a06.webp","price":1500,"imeis":["356938035643809","490154203237518"]}`))
	create.Header.Set("x-pay2own-token", "pay2own-local-dev")
	rec := httptest.NewRecorder()
	service.create(log).ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}
	if !book.phones[0].PriceMinor.Valid || book.phones[0].PriceMinor.Int64 != 150000 {
		t.Fatalf("price minor = %#v", book.phones[0].PriceMinor)
	}

	if book.phones[0].Available != 2 || !book.phones[0].InStock {
		t.Fatalf("available = %d", book.phones[0].Available)
	}
	if !strings.Contains(rec.Body.String(), `"available":2`) {
		t.Fatalf("response = %s", rec.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPut, "/api/catalog/phones/row_testphone000001", strings.NewReader(`{"name":"Test Phone","short":"Test","imeis":["123"]}`))
	bad.SetPathValue("id", "row_testphone000001")
	bad.Header.Set("x-pay2own-token", "pay2own-local-dev")
	rec = httptest.NewRecorder()
	service.save(log).ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad imei status %d", rec.Code)
	}

	list := httptest.NewRecorder()
	service.list(log).ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/catalog/phones", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Test Phone") {
		t.Fatalf("list status %d body %s", list.Code, list.Body.String())
	}
}

func TestCatalogBlankPriceStaysUnset(t *testing.T) {
	book := &memoryCatalog{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := CatalogService{Store: book, Token: "pay2own-local-dev"}
	req := httptest.NewRequest(http.MethodPost, "/api/catalog/phones", strings.NewReader(`{"name":"No Price","short":"None","inStock":true}`))
	req.Header.Set("x-pay2own-token", "pay2own-local-dev")
	rec := httptest.NewRecorder()
	service.create(log).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if book.phones[0].PriceMinor.Valid {
		t.Fatal("blank price was stored")
	}
	if book.phones[0].Image != "/assets/icon.svg" {
		t.Fatalf("image %s", book.phones[0].Image)
	}
}
