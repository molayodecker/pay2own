package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrNotFound means the row we asked for doesn't exist.
var ErrNotFound = errors.New("not found")
var ErrAlreadyPending = errors.New("payment already pending")
var ErrAmountMismatch = errors.New("payment amount mismatch")

type NextInstallment struct {
	IMEI        string `json:"imei"`
	Sequence    int    `json:"sequence"`
	DueDate     string `json:"due_date"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
}

type PendingPayment struct {
	Reference     string
	InstallmentID int64
	Currency      string
	AmountMinor   int64
	CustomerID    int64
	Phone         string
	Provider      string
}

// Store wraps the database. Everything that talks to Postgres goes through it.
type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) NextInstallment(ctx context.Context, imei string) (NextInstallment, error) {
	const q = `
SELECT d.imei, i.sequence, to_char(i.due_date, 'YYYY-MM-DD'), l.currency, i.amount_minor, i.status
FROM devices d
JOIN loans l ON l.device_id = d.id
JOIN installments i ON i.loan_id = l.id
WHERE d.imei = $1 AND l.status = 'active' AND i.status = 'unpaid'
ORDER BY i.sequence
LIMIT 1`
	var n NextInstallment
	err := s.db.QueryRowContext(ctx, q, imei).Scan(&n.IMEI, &n.Sequence, &n.DueDate, &n.Currency, &n.AmountMinor, &n.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

func (s *Store) PayNextInstallment(ctx context.Context, imei string) (NextInstallment, error) {
	const q = `
WITH next AS (
	SELECT i.id, d.imei, l.currency
	FROM devices d
	JOIN loans l ON l.device_id = d.id
	JOIN installments i ON i.loan_id = l.id
	WHERE d.imei = $1 AND l.status = 'active' AND i.status = 'unpaid'
	ORDER BY i.sequence
	LIMIT 1
	FOR UPDATE OF i
)
UPDATE installments i
SET status = 'paid', paid_at = now()
FROM next
WHERE i.id = next.id
RETURNING next.imei, i.sequence, to_char(i.due_date, 'YYYY-MM-DD'), next.currency, i.amount_minor, i.status`
	var n NextInstallment
	err := s.db.QueryRowContext(ctx, q, imei).Scan(&n.IMEI, &n.Sequence, &n.DueDate, &n.Currency, &n.AmountMinor, &n.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// HashToken returns the SHA-256 fingerprint stored instead of the token.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// IssueDeviceToken creates a new random token for a device, stores only its
// hash, and returns the token. Issuing again replaces (revokes) the old one.
func (s *Store) IssueDeviceToken(ctx context.Context, imei string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET token_hash = $1 WHERE imei = $2`, HashToken(token), imei)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", err
	} else if n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// DeviceIMEIByToken returns the IMEI of the device that owns this token.
func (s *Store) DeviceIMEIByToken(ctx context.Context, token string) (string, error) {
	var imei string
	err := s.db.QueryRowContext(ctx,
		`SELECT imei FROM devices WHERE token_hash = $1`, HashToken(token)).Scan(&imei)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return imei, err
}

// StartPayment creates a pending payment for the device's next unpaid installment.
func (s *Store) StartPayment(ctx context.Context, imei string) (PendingPayment, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return PendingPayment{}, err
	}
	p := PendingPayment{Reference: "p2o_" + hex.EncodeToString(b)}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PendingPayment{}, err
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
	SELECT i.id, l.currency, i.amount_minor, c.id, c.phone, c.momo_provider
	FROM installments i
	JOIN loans l ON l.id = i.loan_id
	JOIN devices d ON d.id = l.device_id
	JOIN customers c ON c.id = d.customer_id
	WHERE d.imei = $1 AND l.status = 'active' AND i.status = 'unpaid'
	ORDER BY i.sequence
	LIMIT 1
	FOR UPDATE OF i`, imei).Scan(&p.InstallmentID, &p.Currency, &p.AmountMinor,
		&p.CustomerID, &p.Phone, &p.Provider)

	if errors.Is(err, sql.ErrNoRows) {
		return PendingPayment{}, ErrNotFound
	}
	if err != nil {
		return PendingPayment{}, err
	}

	var pending bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM payments
		  WHERE installment_id = $1 AND status = 'pending'
		  AND created_at > now() - interval '10 minutes')`, p.InstallmentID).Scan(&pending); err != nil {
		return PendingPayment{}, err
	}
	if pending {
		return PendingPayment{}, ErrAlreadyPending
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO payments (installment_id, reference, currency, amount_minor)
		VALUES ($1, $2, $3, $4)`, p.InstallmentID, p.Reference, p.Currency, p.AmountMinor); err != nil {
		return PendingPayment{}, err
	}
	return p, tx.Commit()
}

// FailPayment marks a pending payment failed so the customer can try again right away.
func (s *Store) FailPayment(ctx context.Context, reference string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payments SET status = 'failed', completed_at = now()
		WHERE reference = $1 AND status = 'pending'`, reference)
	return err
}

// CompletePayment records a verified successful payment and marks its installment paid.
// Calling it again for the same reference does nothing.
func (s *Store) CompletePayment(ctx context.Context, reference string, amountMinor int64, currency string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var installmentID, wantAmount int64
	var wantCurrency, status string
	err = tx.QueryRowContext(ctx, `
		SELECT installment_id, amount_minor, currency, status
		FROM payments WHERE reference = $1 FOR UPDATE`, reference).
		Scan(&installmentID, &wantAmount, &wantCurrency, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "success" {
		return nil // already recorded
	}
	if amountMinor != wantAmount || currency != wantCurrency {
		return ErrAmountMismatch
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE payments SET status = 'success', completed_at = now() WHERE reference = $1`, reference); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE installments SET status = 'paid', paid_at = now()
		WHERE id = $1 AND status = 'unpaid'`, installmentID); err != nil {
		return err
	}
	return tx.Commit()
}
