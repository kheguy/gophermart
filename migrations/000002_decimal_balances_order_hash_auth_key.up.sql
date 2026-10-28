ALTER TABLE users
    ALTER COLUMN balance TYPE NUMERIC USING balance::numeric / 100,
    ALTER COLUMN withdrawn TYPE NUMERIC USING withdrawn::numeric / 100;

ALTER TABLE orders
    ALTER COLUMN accrual TYPE NUMERIC USING accrual::numeric / 100,
    ADD COLUMN number_hash BYTEA;

UPDATE orders SET number_hash = sha256(convert_to(number, 'UTF8'));

ALTER TABLE orders
    DROP CONSTRAINT orders_pkey,
    ALTER COLUMN number_hash SET NOT NULL,
    ADD PRIMARY KEY (number_hash),
    ADD CONSTRAINT orders_number_hash_length CHECK (octet_length(number_hash) = 32);

ALTER TABLE withdrawals
    ALTER COLUMN amount TYPE NUMERIC USING amount::numeric / 100;

CREATE TABLE auth_keys (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    secret BYTEA NOT NULL CHECK (octet_length(secret) = 32)
);
