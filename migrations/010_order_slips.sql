-- +goose Up
-- Order slips from the website. Mithril is not the store for these rows.
CREATE TABLE order_slips (
    id               BIGSERIAL PRIMARY KEY,
    reference        TEXT NOT NULL UNIQUE,
    customer_name    TEXT NOT NULL,
    customer_phone   TEXT NOT NULL,
    worker_id        TEXT,
    phone_model      TEXT NOT NULL,
    visit_window     TEXT NOT NULL,
    mithril_account  BOOLEAN,
    worker_channel   TEXT,
    support_channel  TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT order_slips_worker_channel_check CHECK (worker_channel IS NULL OR worker_channel IN ('whatsapp', 'sms', 'failed')),
    CONSTRAINT order_slips_support_channel_check CHECK (support_channel IS NULL OR support_channel IN ('whatsapp', 'sms', 'failed'))
);

CREATE INDEX order_slips_created_at ON order_slips (created_at DESC);
-- +goose Down
DROP TABLE order_slips;
