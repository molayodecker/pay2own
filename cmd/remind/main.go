package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"pay2own.com/backend/internal/notify"
	"pay2own.com/backend/internal/remind"
	"pay2own.com/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("reminders stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	sender, err := notify.FromEnv(
		os.Getenv("FIREBASE_SERVICE_ACCOUNT_FILE"),
		os.Getenv("TWILIO_ACCOUNT_SID"),
		os.Getenv("TWILIO_AUTH_TOKEN"),
		os.Getenv("TWILIO_MESSAGING_SERVICE_SID"),
		notify.TwilioFromNumber(os.Getenv("TWILIO_PHONE_NUMBER"), os.Getenv("TWILIO_FROM_NUMBER")),
	)
	if err != nil {
		return err
	}
	if sender == nil {
		return errors.New("set FIREBASE_SERVICE_ACCOUNT_FILE or Twilio (TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, and TWILIO_MESSAGING_SERVICE_SID or TWILIO_PHONE_NUMBER)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	st := store.New(db)

	rows, err := st.DueReminders(ctx)
	if err != nil {
		return err
	}
	candidates := make([]remind.Candidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, remind.Candidate{
			InstallmentID: row.InstallmentID,
			Sequence:      row.Sequence,
			Count:         row.Count,
			Due:           row.Due,
			AmountMinor:   row.AmountMinor,
			Currency:      row.Currency,
			PushToken:     row.PushToken,
			Phone:         row.Phone,
		})
	}
	sent, err := remind.Run(ctx, candidates, claimAdapter{st}, sender, store.TodayAccra(time.Now()))
	log.Info("reminders finished", "sent", sent, "considered", len(candidates))
	return err
}

type claimAdapter struct{ *store.Store }

func (c claimAdapter) Claim(ctx context.Context, installmentID int64, kind, channel string, day time.Time) (bool, error) {
	return c.ClaimReminder(ctx, installmentID, kind, channel, day)
}

func (c claimAdapter) Release(ctx context.Context, installmentID int64, kind, channel string, day time.Time) error {
	return c.ReleaseReminder(ctx, installmentID, kind, channel, day)
}
