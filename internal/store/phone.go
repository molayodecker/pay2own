package store

import (
	"strings"
	"time"
)

// MaskPhone keeps the first three and last four characters.
// 0551234987 becomes 055•••4987. Shorter values are fully hidden.
func MaskPhone(phone string) string {
	r := []rune(phone)
	if len(r) < 8 {
		return "••••"
	}
	return string(r[:3]) + strings.Repeat("•", len(r)-7) + string(r[len(r)-4:])
}

// AccountStatus is the badge the customer app shows.
// due_soon means the next installment is due today or tomorrow.
func AccountStatus(loanStatus, nextDue string, today time.Time) string {
	if loanStatus == "paid_off" || nextDue == "" {
		return "paid_off"
	}
	due, err := time.Parse("2006-01-02", nextDue)
	if err != nil {
		return "up_to_date"
	}
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	due = time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case due.Before(today):
		return "overdue"
	case !due.After(today.AddDate(0, 0, 1)):
		return "due_soon"
	default:
		return "up_to_date"
	}
}

// TodayAccra is the calendar date in Ghana, where the loans are due.
func TodayAccra(now time.Time) time.Time {
	loc, err := time.LoadLocation("Africa/Accra")
	if err != nil {
		loc = time.FixedZone("GMT", 0)
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
