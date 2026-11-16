package remind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPlan(t *testing.T) {
	today := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	rows := []Candidate{
		{InstallmentID: 1, Sequence: 3, Count: 8, Due: today.AddDate(0, 0, 2), AmountMinor: 30000, Currency: "GHS", Phone: "0551234987"},
		{InstallmentID: 2, Sequence: 3, Count: 8, Due: today.AddDate(0, 0, 1), AmountMinor: 30000, Currency: "GHS", PushToken: "tok", Phone: "0551234987"},
		{InstallmentID: 3, Sequence: 4, Count: 8, Due: today, AmountMinor: 30000, Currency: "GHS", Phone: "0551234987"},
		{InstallmentID: 4, Sequence: 2, Count: 8, Due: today.AddDate(0, 0, -3), AmountMinor: 30000, Currency: "GHS", Phone: "0551234987"},
	}
	got := Plan(today, rows)
	if len(got) != 3 {
		t.Fatalf("planned %d, want 3", len(got))
	}
	if got[0].Kind != Upcoming || got[1].Kind != DueToday || got[2].Kind != Overdue {
		t.Fatalf("kinds %s %s %s", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	for _, item := range got {
		if strings.Contains(item.Body, "0551234987") {
			t.Fatalf("reminder body contains the phone number: %s", item.Body)
		}
	}
	if !strings.Contains(got[0].Body, "3 of 8") || !strings.Contains(got[0].Body, "GHS 300.00") {
		t.Fatalf("body %q", got[0].Body)
	}
}

type memClaims struct {
	held map[string]bool
}

func claimKey(id int64, kind, channel string) string {
	return fmt.Sprintf("%d|%s|%s", id, kind, channel)
}

func (m *memClaims) Claim(_ context.Context, id int64, kind, channel string, _ time.Time) (bool, error) {
	if m.held == nil {
		m.held = map[string]bool{}
	}
	k := claimKey(id, kind, channel)
	if m.held[k] {
		return false, nil
	}
	m.held[k] = true
	return true, nil
}

func (m *memClaims) Release(_ context.Context, id int64, kind, channel string, _ time.Time) error {
	delete(m.held, claimKey(id, kind, channel))
	return nil
}

type scripted struct {
	pushErr error
	smsErr  error
	pushes  int
	texts   int
}

func (s *scripted) SendPush(context.Context, string, string, string) error {
	s.pushes++
	return s.pushErr
}

func (s *scripted) SendSMS(context.Context, string, string) error {
	s.texts++
	return s.smsErr
}

func TestRunFallsBackToSMS(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	rows := []Candidate{{
		InstallmentID: 9, Sequence: 1, Count: 4,
		Due: today.AddDate(0, 0, 1), AmountMinor: 1000, Currency: "GHS",
		PushToken: "tok", Phone: "0550000000",
	}}
	sender := &scripted{pushErr: errors.New("rejected")}
	sent, err := Run(context.Background(), rows, &memClaims{}, sender, today)
	if err != nil || sent != 1 || sender.pushes != 1 || sender.texts != 1 {
		t.Fatalf("sent=%d err=%v pushes=%d texts=%d", sent, err, sender.pushes, sender.texts)
	}
}

func TestRunDoesNotSendTwice(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	rows := []Candidate{{
		InstallmentID: 4, Sequence: 1, Count: 2,
		Due: today, AmountMinor: 500, Currency: "GHS", Phone: "0550000000",
	}}
	sender := &scripted{}
	claims := &memClaims{}
	if _, err := Run(context.Background(), rows, claims, sender, today); err != nil {
		t.Fatal(err)
	}
	sent, err := Run(context.Background(), rows, claims, sender, today)
	if err != nil || sent != 0 || sender.texts != 1 {
		t.Fatalf("sent=%d err=%v texts=%d", sent, err, sender.texts)
	}
}

func TestSMSFallbackSuppressesLaterPush(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	rows := []Candidate{{
		InstallmentID: 11, Sequence: 1, Count: 4,
		Due: today, AmountMinor: 1000, Currency: "GHS",
		PushToken: "tok", Phone: "0550000000",
	}}
	claims := &memClaims{}
	sender := &scripted{pushErr: errors.New("temporarily down")}
	if sent, err := Run(context.Background(), rows, claims, sender, today); err != nil || sent != 1 {
		t.Fatalf("fallback sent=%d err=%v", sent, err)
	}
	sender.pushErr = nil
	if sent, err := Run(context.Background(), rows, claims, sender, today); err != nil || sent != 0 {
		t.Fatalf("rerun sent=%d err=%v", sent, err)
	}
	if sender.pushes != 1 || sender.texts != 1 {
		t.Fatalf("duplicate delivery: pushes=%d sms=%d", sender.pushes, sender.texts)
	}
}

func TestSMSOnlyThenPushRegistrationDoesNotResend(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	rows := []Candidate{{InstallmentID: 12, Due: today, Phone: "0550000000"}}
	claims := &memClaims{}
	sender := &scripted{}
	if sent, err := Run(context.Background(), rows, claims, sender, today); err != nil || sent != 1 {
		t.Fatalf("initial sent=%d err=%v", sent, err)
	}
	rows[0].PushToken = "new-token"
	if sent, err := Run(context.Background(), rows, claims, sender, today); err != nil || sent != 0 {
		t.Fatalf("rerun sent=%d err=%v", sent, err)
	}
	if sender.texts != 1 || sender.pushes != 0 {
		t.Fatalf("duplicate delivery: pushes=%d sms=%d", sender.pushes, sender.texts)
	}
}
