-- +goose Up
ALTER TABLE devices
  ADD COLUMN token_hash BYTEA UNIQUE;
-- +goose Down
ALTER TABLE devices DROP COLUMN token_hash;
