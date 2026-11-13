CREATE TABLE loans (
    id                 BIGSERIAL PRIMARY KEY,
    device_id          BIGINT NOT NULL UNIQUE REFERENCES devices(id),
    currency           TEXT NOT NULL DEFAULT 'GHS' CHECK (currency = 'GHS'),
    principal_minor    BIGINT NOT NULL CHECK (principal_minor > 0),
    deposit_minor      BIGINT NOT NULL DEFAULT 0 CHECK (deposit_minor >= 0),
    status             TEXT NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'paid_off', 'cancelled')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (deposit_minor < principal_minor)
);