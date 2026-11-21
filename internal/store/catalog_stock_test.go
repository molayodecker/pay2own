package store

import (
	"context"
	"testing"
)

func TestOrderHoldsOneIMEIAndCancelPutsItBack(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	phone := CatalogPhone{
		ID:          "row_stocktest00001",
		Name:        "Stock Test",
		Short:       "Stock",
		Description: "A handset used to prove the counter.",
		Image:       "/assets/icon.svg",
		IMEIs:       []string{"356938035643809", "490154203237518"},
	}
	t.Cleanup(func() {
		_, _ = st.db.ExecContext(context.Background(), `DELETE FROM order_slips WHERE catalog_phone_id = $1`, phone.ID)
		_, _ = st.db.ExecContext(context.Background(), `DELETE FROM catalog_phones WHERE id = $1`, phone.ID)
	})

	saved, err := st.SaveCatalog(ctx, phone)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Available != 2 || !saved.InStock {
		t.Fatalf("available after save = %d", saved.Available)
	}

	order, err := st.InsertOrder(ctx, OrderSlip{
		Name: "Ama Mensah", Phone: "0241112233", CatalogPhoneID: phone.ID, PhoneModel: phone.Name, Visit: "Next week",
	})
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.IMEI == "" || order.Status != "open" {
		t.Fatalf("reserved %#v", order)
	}
	if err := st.DeleteCatalog(ctx, phone.ID); err != ErrCatalogReserved {
		t.Fatalf("reserved catalog deletion err = %v, want %v", err, ErrCatalogReserved)
	}

	after, err := st.GetCatalog(ctx, phone.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Available != 1 {
		t.Fatalf("available after order = %d", after.Available)
	}

	_, err = st.InsertOrder(ctx, OrderSlip{
		Name: "Kofi Boateng", Phone: "0240000000", CatalogPhoneID: phone.ID, PhoneModel: phone.Name, Visit: "Next week",
	})
	if err != nil {
		t.Fatalf("second order: %v", err)
	}
	empty, err := st.GetCatalog(ctx, phone.ID)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if empty.Available != 0 || empty.InStock {
		t.Fatalf("available when empty = %d inStock %v", empty.Available, empty.InStock)
	}
	if _, err := st.InsertOrder(ctx, OrderSlip{
		Name: "Esi Mensah", Phone: "0242223344", CatalogPhoneID: phone.ID, PhoneModel: phone.Name, Visit: "Next week",
	}); err != ErrOutOfStock {
		t.Fatalf("third order err = %v", err)
	}

	cancelled, err := st.CancelOrder(ctx, order.Reference)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("status %s", cancelled.Status)
	}
	restored, err := st.GetCatalog(ctx, phone.ID)
	if err != nil {
		t.Fatalf("get restored: %v", err)
	}
	if restored.Available != 1 {
		t.Fatalf("available after cancel = %d", restored.Available)
	}
	if err := st.DeleteCatalog(ctx, phone.ID); err != ErrCatalogReserved {
		t.Fatalf("second open order should prevent deletion: %v", err)
	}
}
