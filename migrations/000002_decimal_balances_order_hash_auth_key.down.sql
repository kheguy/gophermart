DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE balance * 100 <> trunc(balance * 100) OR withdrawn * 100 <> trunc(withdrawn * 100))
        OR EXISTS (SELECT 1 FROM orders WHERE accrual * 100 <> trunc(accrual * 100))
        OR EXISTS (SELECT 1 FROM withdrawals WHERE amount * 100 <> trunc(amount * 100)) THEN
        RAISE EXCEPTION 'cannot restore whole hundredths without losing decimal amounts';
    END IF;
END
$$;

DROP TABLE auth_keys;

ALTER TABLE users
    ALTER COLUMN balance TYPE BIGINT USING (balance * 100)::bigint,
    ALTER COLUMN withdrawn TYPE BIGINT USING (withdrawn * 100)::bigint;

ALTER TABLE orders
    DROP CONSTRAINT orders_pkey,
    DROP CONSTRAINT orders_number_hash_length,
    DROP COLUMN number_hash,
    ADD PRIMARY KEY (number),
    ALTER COLUMN accrual TYPE BIGINT USING (accrual * 100)::bigint;

ALTER TABLE withdrawals
    ALTER COLUMN amount TYPE BIGINT USING (amount * 100)::bigint;
