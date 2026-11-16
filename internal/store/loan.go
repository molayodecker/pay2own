package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Installment is one row of the customer's schedule.
type Installment struct {
	Sequence    int    `json:"sequence"`
	DueDate     string `json:"due_date"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
}

// LoanSummary is everything the home, schedule and payment screens need.
// The phone number is already masked. The IMEI is not included.
type LoanSummary struct {
	CustomerName     string        `json:"customer_name"`
	PhoneMasked      string        `json:"phone_masked"`
	MomoProvider     string        `json:"momo_provider"`
	Model            string        `json:"model"`
	Currency         string        `json:"currency"`
	PriceMinor       int64         `json:"price_minor"`
	DepositMinor     int64         `json:"deposit_minor"`
	RemainingMinor   int64         `json:"remaining_minor"`
	PaidMinor        int64         `json:"paid_minor"`
	StartedOn        string        `json:"started_on"`
	LoanStatus       string        `json:"loan_status"`
	AccountStatus    string        `json:"account_status"`
	InstallmentCount int           `json:"installment_count"`
	PaidCount        int           `json:"paid_count"`
	Next             *Installment  `json:"next"`
	PendingReference *string       `json:"pending_reference"`
	Schedule         []Installment `json:"schedule"`
	Terms            string        `json:"terms"`
}

// PaymentRecord is one row of the customer's payment history.
type PaymentRecord struct {
	Reference   string  `json:"reference"`
	Currency    string  `json:"currency"`
	AmountMinor int64   `json:"amount_minor"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
	Sequences   []int   `json:"sequences"`
}

// PaymentContact is who to tell that a payment landed. It is not sent to the app.
type PaymentContact struct {
	PushToken   string
	Phone       string
	AmountMinor int64
	Currency    string
	Reference   string
}

// ReminderRow is the earliest unpaid installment for one active loan.
type ReminderRow struct {
	InstallmentID int64
	Sequence      int
	Count         int
	Due           time.Time
	AmountMinor   int64
	Currency      string
	PushToken     string
	Phone         string
}

func (s *Store) LoanSummary(ctx context.Context, imei string) (LoanSummary, error) {
	var sum LoanSummary
	var phone string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.full_name, c.phone, c.momo_provider, d.model,
		       l.currency, l.principal_minor, l.deposit_minor, l.status,
		       to_char(l.created_at AT TIME ZONE 'Africa/Accra', 'YYYY-MM-DD')
		FROM devices d
		JOIN customers c ON c.id = d.customer_id
		JOIN loans l ON l.device_id = d.id
		WHERE d.imei = $1`, imei).Scan(
		&sum.CustomerName, &phone, &sum.MomoProvider, &sum.Model,
		&sum.Currency, &sum.PriceMinor, &sum.DepositMinor, &sum.LoanStatus, &sum.StartedOn)
	if errors.Is(err, sql.ErrNoRows) {
		return LoanSummary{}, ErrNotFound
	}
	if err != nil {
		return LoanSummary{}, err
	}
	sum.PhoneMasked = MaskPhone(phone)
	sum.Terms = CustomerTerms
	sum.PaidMinor = sum.DepositMinor

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.sequence, to_char(i.due_date, 'YYYY-MM-DD'), i.amount_minor, i.status
		FROM installments i
		JOIN loans l ON l.id = i.loan_id
		JOIN devices d ON d.id = l.device_id
		WHERE d.imei = $1
		ORDER BY i.sequence`, imei)
	if err != nil {
		return LoanSummary{}, err
	}
	defer rows.Close()
	sum.Schedule = []Installment{}
	var nextDue string
	for rows.Next() {
		var item Installment
		if err := rows.Scan(&item.Sequence, &item.DueDate, &item.AmountMinor, &item.Status); err != nil {
			return LoanSummary{}, err
		}
		sum.Schedule = append(sum.Schedule, item)
		sum.InstallmentCount++
		if item.Status == "paid" {
			sum.PaidCount++
			sum.PaidMinor += item.AmountMinor
			continue
		}
		sum.RemainingMinor += item.AmountMinor
		if sum.Next == nil {
			copied := item
			sum.Next = &copied
			nextDue = item.DueDate
		}
	}
	if err := rows.Err(); err != nil {
		return LoanSummary{}, err
	}
	sum.AccountStatus = AccountStatus(sum.LoanStatus, nextDue, TodayAccra(time.Now()))

	var pending sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT p.reference
		FROM payments p
		JOIN installments i ON i.id = p.installment_id
		JOIN loans l ON l.id = i.loan_id
		JOIN devices d ON d.id = l.device_id
		WHERE d.imei = $1 AND p.status = 'pending'
		  
		ORDER BY p.created_at DESC
		LIMIT 1`, imei).Scan(&pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoanSummary{}, err
	}
	if pending.Valid {
		sum.PendingReference = &pending.String
	}
	return sum, nil
}

func (s *Store) ListPayments(ctx context.Context, imei string) ([]PaymentRecord, error) {
	rows, err := s.db.QueryContext(ctx, paymentSelect+` WHERE d.imei = $1 ORDER BY p.created_at DESC LIMIT 100`, imei)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PaymentRecord{}
	for rows.Next() {
		rec, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) Payment(ctx context.Context, imei, reference string) (PaymentRecord, error) {
	row := s.db.QueryRowContext(ctx, paymentSelect+` WHERE d.imei = $1 AND p.reference = $2`, imei, reference)
	rec, err := scanPayment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentRecord{}, ErrNotFound
	}
	return rec, err
}

const paymentSelect = `
SELECT p.reference, p.currency, p.amount_minor, p.status, p.created_at, p.completed_at,
  COALESCE((
    SELECT string_agg(i2.sequence::text, ',' ORDER BY i2.sequence)
    FROM payment_installments pi
    JOIN installments i2 ON i2.id = pi.installment_id
    WHERE pi.payment_id = p.id
  ), i.sequence::text)
FROM payments p
JOIN installments i ON i.id = p.installment_id
JOIN loans l ON l.id = i.loan_id
JOIN devices d ON d.id = l.device_id`

type paymentScanner interface {
	Scan(dest ...any) error
}

func scanPayment(row paymentScanner) (PaymentRecord, error) {
	var rec PaymentRecord
	var created time.Time
	var completed sql.NullTime
	var sequences string
	err := row.Scan(&rec.Reference, &rec.Currency, &rec.AmountMinor, &rec.Status, &created, &completed, &sequences)
	if err != nil {
		return PaymentRecord{}, err
	}
	rec.CreatedAt = created.UTC().Format(time.RFC3339)
	if completed.Valid {
		formatted := completed.Time.UTC().Format(time.RFC3339)
		rec.CompletedAt = &formatted
	}
	rec.Sequences = parseSequences(sequences)
	return rec, nil
}

func parseSequences(raw string) []int {
	if raw == "" {
		return []int{}
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (s *Store) SavePushToken(ctx context.Context, imei, token string) error {
	token = strings.TrimSpace(token)
	if len(token) < 20 || len(token) > 4096 || strings.ContainsAny(token, " \r\n") {
		return ErrBadRequest
	}
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET fcm_token = $1 WHERE imei = $2`, token, imei)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PaymentContact(ctx context.Context, reference string) (PaymentContact, error) {
	var c PaymentContact
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(d.fcm_token, ''), c.phone, p.amount_minor, p.currency, p.reference
		FROM payments p
		JOIN installments i ON i.id = p.installment_id
		JOIN loans l ON l.id = i.loan_id
		JOIN devices d ON d.id = l.device_id
		JOIN customers c ON c.id = d.customer_id
		WHERE p.reference = $1 AND p.status = 'success'`, reference).
		Scan(&c.PushToken, &c.Phone, &c.AmountMinor, &c.Currency, &c.Reference)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentContact{}, ErrNotFound
	}
	return c, err
}

func (s *Store) DueReminders(ctx context.Context) ([]ReminderRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (d.id)
		  i.id, i.sequence, i.due_date, i.amount_minor, l.currency,
		  COALESCE(d.fcm_token, ''), c.phone,
		  (SELECT COUNT(*) FROM installments all_i WHERE all_i.loan_id = l.id)
		FROM installments i
		JOIN loans l ON l.id = i.loan_id AND l.status = 'active'
		JOIN devices d ON d.id = l.device_id
		JOIN customers c ON c.id = d.customer_id
		WHERE i.status = 'unpaid'
		ORDER BY d.id, i.sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderRow
	for rows.Next() {
		var row ReminderRow
		if err := rows.Scan(&row.InstallmentID, &row.Sequence, &row.Due, &row.AmountMinor,
			&row.Currency, &row.PushToken, &row.Phone, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ClaimReminder(ctx context.Context, installmentID int64, kind, channel string, day time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO reminder_sends (installment_id, kind, channel, sent_on)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, installmentID, kind, channel, day.Format("2006-01-02"))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) ReleaseReminder(ctx context.Context, installmentID int64, kind, channel string, day time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reminder_sends
		WHERE installment_id = $1 AND kind = $2 AND channel = $3 AND sent_on = $4`,
		installmentID, kind, channel, day.Format("2006-01-02"))
	return err
}
