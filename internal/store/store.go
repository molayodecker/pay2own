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

type NextInstallment struct {
	IMEI        string `json:"imei"`
	Sequence    int    `json:"sequence"`
	DueDate     string `json:"due_date"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
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