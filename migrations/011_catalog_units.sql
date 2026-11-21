-- +goose Up
-- Each row is one physical handset. The available rows are the stock count.
CREATE TABLE catalog_phone_units (
    id                BIGSERIAL PRIMARY KEY,
    catalog_phone_id  TEXT NOT NULL REFERENCES catalog_phones(id) ON DELETE CASCADE,
    imei              TEXT NOT NULL UNIQUE CHECK (imei ~ '^[0-9]{15}$'),
    status            TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'reserved')),
    order_reference   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX catalog_phone_units_available ON catalog_phone_units (catalog_phone_id) WHERE status = 'available';

ALTER TABLE order_slips
    ADD COLUMN catalog_phone_id TEXT,
    ADD COLUMN imei TEXT,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'cancelled'));

UPDATE catalog_phones SET in_stock = false;

-- +goose Down
ALTER TABLE order_slips DROP COLUMN status, DROP COLUMN imei, DROP COLUMN catalog_phone_id;
DROP TABLE catalog_phone_units;
