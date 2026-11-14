-- +goose Up
ALTER TABLE customers
    ADD COLUMN momo_provider TEXT NOT NULL DEFAULT 'mtn'
    CHECK (momo_provider IN ('mtn', 'vod', 'atl'));

-- +goose Down
ALTER TABLE customers DROP COLUMN momo_provider;