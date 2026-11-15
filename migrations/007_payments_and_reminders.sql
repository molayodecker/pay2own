-- +goose Up
-- One payment can cover several installments, or the remaining balance.
CREATE TABLE payment_installments (
    payment_id      BIGINT NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    installment_id  BIGINT NOT NULL REFERENCES installments(id),
    amount_minor    BIGINT NOT NULL CHECK (amount_minor > 0),
    PRIMARY KEY (payment_id, installment_id)
);

INSERT INTO payment_installments (payment_id, installment_id, amount_minor)
SELECT id, installment_id, amount_minor FROM payments;

CREATE TABLE reminder_sends (
    installment_id  BIGINT NOT NULL REFERENCES installments(id),
    kind            TEXT NOT NULL CHECK (kind IN ('upcoming', 'due_today', 'overdue')),
    channel         TEXT NOT NULL CHECK (channel IN ('push', 'sms')),
    sent_on         DATE NOT NULL,
    PRIMARY KEY (installment_id, kind, channel, sent_on)
);

ALTER TABLE devices ADD COLUMN fcm_token TEXT;

-- +goose Down
ALTER TABLE devices DROP COLUMN fcm_token;
DROP TABLE reminder_sends;
DROP TABLE payment_installments;
