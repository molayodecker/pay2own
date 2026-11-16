package store

import (
	"strings"
	"testing"
	"time"
)

func TestMaskPhone(t *testing.T) {
	got := MaskPhone("0551234987")
	if got != "055•••4987" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "123") {
		t.Fatal("middle of the number leaked")
	}
	if got := MaskPhone("+233000000001"); got != "+23••••••0001" || strings.Contains(got, "300000") {
		t.Fatalf("got %q", got)
	}
	if got := MaskPhone("1234"); got != "••••" {
		t.Fatalf("short number: %q", got)
	}
}

func TestAccountStatus(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		loan, due, want string
	}{
		{"paid_off", "", "paid_off"},
		{"active", "", "paid_off"},
		{"active", "2026-11-13", "overdue"},
		{"active", "2026-11-14", "due_soon"},
		{"active", "2026-11-15", "due_soon"},
		{"active", "2026-11-16", "up_to_date"},
	}
	for _, tc := range cases {
		if got := AccountStatus(tc.loan, tc.due, today); got != tc.want {
			t.Errorf("AccountStatus(%s, %s) = %s, want %s", tc.loan, tc.due, got, tc.want)
		}
	}
}
