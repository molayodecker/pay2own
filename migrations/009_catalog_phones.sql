-- +goose Up
-- Phones shown on pay2own-web. These are models for sale, not financed handsets.
CREATE TABLE catalog_phones (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    short_name    TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    image_path    TEXT NOT NULL,
    price_minor   BIGINT CHECK (price_minor IS NULL OR price_minor >= 0),
    in_stock      BOOLEAN NOT NULL DEFAULT true,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO catalog_phones (id, name, short_name, description, image_path, sort_order) VALUES
    (
        'row_cf3cee3ae60dca28f33d',
        'Samsung Galaxy A06',
        'Galaxy A06',
        'A 6.7-inch screen, a 50MP main camera and a 5,000mAh battery.',
        '/assets/phones/galaxy-a06.webp',
        1
    ),
    (
        'row_b45453af20175237aca4',
        'Samsung Galaxy A50',
        'Galaxy A50',
        'A 6.4-inch Super AMOLED screen, a 25MP main camera and an in-screen fingerprint reader.',
        '/assets/phones/galaxy-a50.webp',
        2
    ),
    (
        'row_77c52e7ee9bbbd0b7a19',
        'Tecno Spark Go 3',
        'Spark Go 3',
        'A 6.75-inch 120Hz screen, a 13MP rear camera and a 5,000mAh battery.',
        '/assets/phones/spark-go-3.webp',
        3
    );

-- +goose Down
DROP TABLE catalog_phones;
