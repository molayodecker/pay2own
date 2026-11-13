CREATE TABLE installments (
    id            BIGSERIAL PRIMARY KEY,
    loan_id       BIGINT NOT NULL REFERENCES loans(id),
    sequence      INT NOT NULL CHECK (sequence > 0),
    due_date      DATE NOT NULL,
    amount_minor  BIGINT NOT NULL CHECK (amount_minor > 0),
    status        TEXT NOT NULL DEFAULT 'unpaid'
                  CHECK (status IN ('unpaid', 'paid')),
    paid_at       TIMESTAMPTZ,
    UNIQUE (loan_id, sequence),
    CHECK ((status = 'paid') = (paid_at IS NOT NULL))
);