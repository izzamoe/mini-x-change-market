CREATE TABLE IF NOT EXISTS trades (
    id             UUID         PRIMARY KEY,
    stock_code     VARCHAR(10)  NOT NULL,
    buy_order_id   UUID         NOT NULL REFERENCES orders(id),
    sell_order_id  UUID         NOT NULL REFERENCES orders(id),
    price          BIGINT       NOT NULL,
    quantity       BIGINT       NOT NULL,
    buyer_user_id  VARCHAR(255) NOT NULL,
    seller_user_id VARCHAR(255) NOT NULL,
    executed_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trades_stock_code   ON trades(stock_code);
CREATE INDEX IF NOT EXISTS idx_trades_executed_at  ON trades(executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_buy_order_id ON trades(buy_order_id);
