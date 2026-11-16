-- +goose Up
ALTER TABLE devices
  ADD COLUMN restriction_state TEXT NOT NULL DEFAULT 'not_enrolled'
    CHECK (restriction_state IN ('not_enrolled', 'active', 'release_pending', 'released')),
  ADD COLUMN released_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE devices
  DROP COLUMN released_at,
  DROP COLUMN restriction_state;