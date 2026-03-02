CREATE TABLE IF NOT EXISTS orders (
    id              UUID         PRIMARY KEY,
    user_id         VARCHAR(255) NOT NULL,
    stock_code      VARCHAR(10)  NOT NULL,
    side            VARCHAR(4)   NOT NULL,
    price           BIGINT       NOT NULL,
    quantity        BIGINT       NOT NULL,
    filled_quantity BIGINT       NOT NULL DEFAULT 0,
    status          VARCHAR(20)  NOT NULL DEFAULT 'OPEN',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_stock_code ON orders(stock_code);
CREATE INDEX IF NOT EXISTS idx_orders_status     ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_user_id    ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at DESC);
