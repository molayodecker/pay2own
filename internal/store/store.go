package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrNotFound means the row we asked for doesn't exist.
var ErrNotFound = errors.New("not found")
var ErrAlreadyPending = errors.New("payment already pending")
var ErrAmountMismatch = errors.New("payment amount mismatch")
var ErrBadRequest = errors.New("bad request")
var ErrNotReleasable = errors.New("device is not pending release")
var ErrNotActivatable = errors.New("device cannot be restricted")

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

// StartPayment creates a pending payment for the next unpaid installments.
// count 1 is the installment due, count above 1 covers that many in order,
// and count 0 covers the remaining balance. A device can have only one
// payment waiting.
func (s *Store) StartPayment(ctx context.Context, imei string, count int) (PendingPayment, error) {
	if count < 0 || count > 104 {
		return PendingPayment{}, ErrBadRequest
	}
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

	q := `
	SELECT i.id, l.currency, i.amount_minor, c.id, c.phone, c.momo_provider
	FROM installments i
	JOIN loans l ON l.id = i.loan_id
	JOIN devices d ON d.id = l.device_id
	JOIN customers c ON c.id = d.customer_id
	WHERE d.imei = $1 AND l.status = 'active' AND i.status = 'unpaid'
	ORDER BY i.sequence
	FOR UPDATE OF i`
	args := []any{imei}
	if count > 0 {
		q += ` LIMIT $2`
		args = append(args, count)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return PendingPayment{}, err
	}
	type part struct {
		id     int64
		amount int64
	}
	var parts []part
	for rows.Next() {
		var id, amount, customerID int64
		var currency, phone, provider string
		if err := rows.Scan(&id, &currency, &amount, &customerID, &phone, &provider); err != nil {
			rows.Close()
			return PendingPayment{}, err
		}
		if len(parts) == 0 {
			p.Currency = currency
			p.CustomerID = customerID
			p.Phone = phone
			p.Provider = provider
		}
		parts = append(parts, part{id: id, amount: amount})
		p.AmountMinor += amount
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PendingPayment{}, err
	}
	rows.Close()
	if len(parts) == 0 {
		return PendingPayment{}, ErrNotFound
	}
	if count > 0 && len(parts) != count {
		return PendingPayment{}, ErrBadRequest
	}
	p.InstallmentID = parts[0].id

	var pending bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM payments p
		  JOIN installments i ON i.id = p.installment_id
		  JOIN loans l ON l.id = i.loan_id
		  JOIN devices d ON d.id = l.device_id
		  WHERE d.imei = $1 AND p.status = 'pending'
		    
		)`, imei).Scan(&pending); err != nil {
		return PendingPayment{}, err
	}
	if pending {
		return PendingPayment{}, ErrAlreadyPending
	}

	var paymentID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO payments (installment_id, reference, currency, amount_minor)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		p.InstallmentID, p.Reference, p.Currency, p.AmountMinor).Scan(&paymentID); err != nil {
		return PendingPayment{}, err
	}
	for _, part := range parts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO payment_installments (payment_id, installment_id, amount_minor)
			VALUES ($1, $2, $3)`, paymentID, part.id, part.amount); err != nil {
			return PendingPayment{}, err
		}
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

// CompletePayment records a verified successful payment and marks every
// installment it covers as paid. The bool is true only the first time.
// Calling it again for the same reference does nothing.
func (s *Store) CompletePayment(ctx context.Context, reference string, amountMinor int64, currency string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var paymentID, installmentID, wantAmount int64
	var wantCurrency, status string
	err = tx.QueryRowContext(ctx, `
		SELECT id, installment_id, amount_minor, currency, status
		FROM payments WHERE reference = $1 FOR UPDATE`, reference).
		Scan(&paymentID, &installmentID, &wantAmount, &wantCurrency, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if status == "success" {
		return false, nil
	}
	if amountMinor != wantAmount || currency != wantCurrency {
		return false, ErrAmountMismatch
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE payments SET status = 'success', completed_at = now() WHERE reference = $1`, reference); err != nil {
		return false, err
	}
	ids, err := coveredInstallments(ctx, tx, paymentID, installmentID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE installments SET status = 'paid', paid_at = now()
			WHERE id = $1 AND status = 'unpaid'`, id); err != nil {
			return false, err
		}
	}
	// If that was the loan's last unpaid installment, ask the phone to unlock.
	// Only devices with active restrictions move; not_enrolled devices stay as they are.
	if _, err = tx.ExecContext(ctx, `
		UPDATE devices d
		SET restriction_state = 'release_pending'
		FROM installments i
		JOIN loans l ON l.id = i.loan_id
		WHERE i.id = $1
		  AND d.id = l.device_id
		  AND d.restriction_state = 'active'
		  AND NOT EXISTS (
		      SELECT 1 FROM installments u
		      WHERE u.loan_id = l.id AND u.paid_at IS NULL
		  )`, installmentID); err != nil {
		return false, fmt.Errorf("mark device release pending: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE loans SET status = 'paid_off'
		WHERE status = 'active'
		  AND id = (SELECT loan_id FROM installments WHERE id = $1)
		  AND NOT EXISTS (
		    SELECT 1 FROM installments WHERE loan_id = loans.id AND status <> 'paid'
		  )`, ids[0]); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func coveredInstallments(ctx context.Context, tx *sql.Tx, paymentID, fallback int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT installment_id FROM payment_installments
		WHERE payment_id = $1 ORDER BY installment_id`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []int64{fallback}, nil
	}
	return ids, nil
}

type Sale struct {
	FullName     string
	Phone        string // MoMo number, e.g. 0551234987
	Provider     string // mtn, vod or atl
	IMEI         string
	Model        string
	PriceMinor   int64
	DepositMinor int64
	Weeks        int
	FirstDue     time.Time
	Restrict     bool // turn financing restrictions on in the same transaction
}

// Schedule splits what's owed into weekly installments; any leftover pesewas go on the last one.
func Schedule(owedMinor int64, weeks int) []int64 {
	each := owedMinor / int64(weeks)
	out := make([]int64, weeks)
	for i := range out {
		out[i] = each
	}
	out[weeks-1] += owedMinor - each*int64(weeks)
	return out
}

// CreateSale records a customer, device, loan and its installments in one transaction.
func (s *Store) CreateSale(ctx context.Context, sale Sale) (loanID int64, err error) {
	if sale.Weeks < 1 || sale.Weeks > 104 || sale.DepositMinor < 0 || sale.DepositMinor >= sale.PriceMinor {
		return 0, errors.New("invalid sale: check price, deposit and weeks")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var customerID, deviceID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO customers (full_name, phone, momo_provider) VALUES ($1, $2, $3)
		ON CONFLICT (phone) DO UPDATE SET full_name = EXCLUDED.full_name,
			momo_provider = EXCLUDED.momo_provider
		RETURNING id`, sale.FullName, sale.Phone, sale.Provider).Scan(&customerID); err != nil {
		return 0, fmt.Errorf("customer: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO devices (customer_id, imei, model) VALUES ($1, $2, $3) RETURNING id`,
		customerID, sale.IMEI, sale.Model).Scan(&deviceID); err != nil {
		return 0, fmt.Errorf("device: %w", err)
	}
	if sale.Restrict {
		if _, err := tx.ExecContext(ctx, `
			UPDATE devices SET restriction_state = 'active' WHERE id = $1`, deviceID); err != nil {
			return 0, fmt.Errorf("restrict device: %w", err)
		}
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO loans (device_id, principal_minor, deposit_minor) VALUES ($1, $2, $3) RETURNING id`,
		deviceID, sale.PriceMinor, sale.DepositMinor).Scan(&loanID); err != nil {
		return 0, fmt.Errorf("loan: %w", err)
	}
	for i, amount := range Schedule(sale.PriceMinor-sale.DepositMinor, sale.Weeks) {
		due := sale.FirstDue.AddDate(0, 0, 7*i)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO installments (loan_id, sequence, due_date, amount_minor) VALUES ($1, $2, $3, $4)`,
			loanID, i+1, due.Format("2006-01-02"), amount); err != nil {
			return 0, fmt.Errorf("installment %d: %w", i+1, err)
		}
	}
	return loanID, tx.Commit()
}

// AdminDevice is the shop view of one financed phone.
type AdminDevice struct {
	IMEI             string
	Model            string
	CustomerName     string
	Phone            string
	Provider         string
	LoanID           int64
	PriceMinor       int64
	DepositMinor     int64
	RestrictionState string
}

// DeviceForAdmin loads the customer, loan, and restriction state for a phone.
func (s *Store) DeviceForAdmin(ctx context.Context, imei string) (AdminDevice, error) {
	var d AdminDevice
	err := s.db.QueryRowContext(ctx, `
		SELECT d.imei, d.model, c.full_name, c.phone, c.momo_provider,
		       l.id, l.principal_minor, l.deposit_minor, d.restriction_state
		FROM devices d
		JOIN customers c ON c.id = d.customer_id
		JOIN loans l ON l.device_id = d.id
		WHERE d.imei = $1`, imei).Scan(
		&d.IMEI, &d.Model, &d.CustomerName, &d.Phone, &d.Provider,
		&d.LoanID, &d.PriceMinor, &d.DepositMinor, &d.RestrictionState,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminDevice{}, ErrNotFound
	}
	return d, err
}

// ActivateDevice turns financing restrictions on. A phone that is already
// active stays active. A phone that is unlocking or already released is left alone.
func (s *Store) ActivateDevice(ctx context.Context, imei string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE devices SET restriction_state = 'active'
		WHERE imei = $1 AND restriction_state = 'not_enrolled'
		  AND EXISTS (
		    SELECT 1 FROM loans l
		    JOIN installments i ON i.loan_id = l.id
		    WHERE l.device_id = devices.id AND l.status = 'active'
		      AND i.status = 'unpaid'
		  )`, imei)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	state, err := s.DeviceRestrictionState(ctx, imei)
	if err != nil {
		return err
	}
	if state == "active" {
		return nil
	}
	return ErrNotActivatable
}

// DeviceRestrictionState returns the restriction state for the device with this IMEI.
func (s *Store) DeviceRestrictionState(ctx context.Context, imei string) (string, error) {
	var state string
	err := s.db.QueryRowContext(ctx,
		`SELECT restriction_state FROM devices WHERE imei = $1`, imei).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return state, err
}

// ConfirmRelease records that the phone removed its restrictions.
// It only moves release_pending to released; calling it again is a no-op.
func (s *Store) ConfirmRelease(ctx context.Context, imei string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET restriction_state = 'released', released_at = now()
		WHERE imei = $1 AND restriction_state = 'release_pending'`, imei)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		state, err := s.DeviceRestrictionState(ctx, imei)
		if err != nil {
			return err
		}
		if state != "released" {
			return ErrNotReleasable
		}
	}
	return nil
}
