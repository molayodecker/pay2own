-- +goose Up
CREATE TABLE payments (
    id              BIGSERIAL PRIMARY KEY,
    installment_id  BIGINT NOT NULL REFERENCES installments(id),
    reference       TEXT NOT NULL UNIQUE,
    provider        TEXT NOT NULL DEFAULT 'paystack' CHECK (provider = 'paystack'),
    currency        TEXT NOT NULL DEFAULT 'GHS' CHECK (currency = 'GHS'),
    amount_minor    BIGINT NOT NULL CHECK (amount_minor > 0),
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'success', 'failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

-- At most one successful payment per installment.
CREATE UNIQUE INDEX payments_one_success_per_installment
    ON payments (installment_id) WHERE status = 'success';

-- +goose Down
DROP TABLE payments;