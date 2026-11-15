package remind

import (
	"context"
	"fmt"
	"time"

	"pay2own.com/backend/internal/notify"
)

const (
	Upcoming = "upcoming"
	DueToday = "due_today"
	Overdue  = "overdue"
)

// Candidate is the earliest unpaid installment for one phone.
type Candidate struct {
	InstallmentID int64
	Sequence      int
	Count         int
	Due           time.Time
	AmountMinor   int64
	Currency      string
	PushToken     string
	Phone         string
}

// Planned is one reminder ready to send.
type Planned struct {
	Candidate
	Kind  string
	Title string
	Body  string
}

// Claims records that a reminder was sent so the daily job can be run twice safely.
type Claims interface {
	Claim(ctx context.Context, installmentID int64, kind, channel string, day time.Time) (bool, error)
	Release(ctx context.Context, installmentID int64, kind, channel string, day time.Time) error
}

// Plan keeps reminders that are due tomorrow, today, or overdue.
func Plan(today time.Time, rows []Candidate) []Planned {
	today = dateOnly(today)
	var out []Planned
	for _, row := range rows {
		kind, ok := kindFor(dateOnly(row.Due), today)
		if !ok {
			continue
		}
		out = append(out, Planned{
			Candidate: row,
			Kind:      kind,
			Title:     title(kind),
			Body:      body(kind, row),
		})
	}
	return out
}

func kindFor(due, today time.Time) (string, bool) {
	switch int(due.Sub(today).Hours() / 24) {
	case 1:
		return Upcoming, true
	case 0:
		return DueToday, true
	default:
		if due.Before(today) {
			return Overdue, true
		}
		return "", false
	}
}

func title(kind string) string {
	switch kind {
	case Upcoming:
		return "Payment due tomorrow"
	case DueToday:
		return "Payment due today"
	default:
		return "Payment overdue"
	}
}

func body(kind string, row Candidate) string {
	amount := formatMoney(row.Currency, row.AmountMinor)
	when := row.Due.Format("2 Jan 2006")
	switch kind {
	case Upcoming:
		return fmt.Sprintf("Installment %d of %d, %s, is due on %s. Open Pay2Own to pay.", row.Sequence, row.Count, amount, when)
	case DueToday:
		return fmt.Sprintf("Installment %d of %d, %s, is due today. Open Pay2Own to pay.", row.Sequence, row.Count, amount)
	default:
		return fmt.Sprintf("Installment %d of %d, %s, was due on %s and is still unpaid. Open Pay2Own to pay.", row.Sequence, row.Count, amount, when)
	}
}

func formatMoney(currency string, minor int64) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	if currency == "" {
		currency = "GHS"
	}
	return fmt.Sprintf("%s%s %d.%02d", sign, currency, minor/100, minor%100)
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Run sends each planned reminder. A failed push falls back to SMS.
// Errors for one phone do not stop the others. sent counts messages that went out.
func Run(ctx context.Context, rows []Candidate, claims Claims, sender notify.Sender, today time.Time) (int, error) {
	today = dateOnly(today)
	sent := 0
	for _, item := range Plan(today, rows) {
		ok, err := deliver(ctx, item, claims, sender, today)
		if err != nil {
			return sent, err
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

func deliver(ctx context.Context, item Planned, claims Claims, sender notify.Sender, today time.Time) (bool, error) {
	channels := []string{"sms"}
	if item.PushToken != "" {
		channels = []string{"push", "sms"}
	}
	for _, channel := range channels {
		claimed, err := claims.Claim(ctx, item.InstallmentID, item.Kind, channel, today)
		if err != nil {
			return false, err
		}
		if !claimed {
			return false, nil
		}
		var sendErr error
		if channel == "push" {
			_, sendErr = notify.Deliver(ctx, pushOnly{sender}, item.PushToken, "", item.Title, item.Body)
		} else {
			_, sendErr = notify.Deliver(ctx, smsOnly{sender}, "", item.Phone, item.Title, item.Body)
		}
		if sendErr == nil {
			return true, nil
		}
		if err := claims.Release(ctx, item.InstallmentID, item.Kind, channel, today); err != nil {
			return false, err
		}
	}
	return false, nil
}

// pushOnly hides SMS so a push attempt cannot skip ahead to the text message.
type pushOnly struct{ notify.Sender }

func (p pushOnly) SendSMS(context.Context, string, string) error { return notify.ErrNotConfigured }

type smsOnly struct{ notify.Sender }

func (s smsOnly) SendPush(context.Context, string, string, string) error {
	return notify.ErrNotConfigured
}
