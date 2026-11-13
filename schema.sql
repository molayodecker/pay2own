CREATE TABLE demo_installments (
    id TEXT PRIMARY KEY,
    currency TEXT NOT NULL CHECK (currency = 'GHS'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('unpaid', 'paid')),
    demo BOOLEAN NOT NULL DEFAULT TRUE CHECK (demo = TRUE)
);

---psql -h localhost -p 5432 -d pay2own_dev -v ON_ERROR_STOP=1 -f schema.sql


