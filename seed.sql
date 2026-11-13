INSERT INTO demo_installments (
    id, currency, amount_minor, status, demo
) VALUES (
    'demo-001', 'GHS', 10000, 'unpaid', TRUE
) ON CONFLICT (id) DO NOTHING;


--- psql -h localhost -p 5432 -d pay2own_dev -v ON_ERROR_STOP=1 -f seed.sql
--- psql -h localhost -p 5432 -d pay2own_dev -c 'SELECT * FROM demo_installments;'