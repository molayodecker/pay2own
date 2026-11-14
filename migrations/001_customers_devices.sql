-- +goose Up
CREATE TABLE customers (
    id          BIGSERIAL PRIMARY KEY,
    full_name   TEXT NOT NULL,
    phone       TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE devices (
    id           BIGSERIAL PRIMARY KEY,
    customer_id  BIGINT NOT NULL REFERENCES customers(id),
    imei         TEXT NOT NULL UNIQUE CHECK (imei ~ '^[0-9]{15}$'),
    model        TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE devices;
DROP TABLE customers;
