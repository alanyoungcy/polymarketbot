CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    market_id TEXT NOT NULL REFERENCES markets(id),
    token_id TEXT NOT NULL,
    wallet TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    order_type TEXT NOT NULL CHECK (order_type IN ('GTC', 'GTD', 'FOK', 'FAK')),
    price_ticks BIGINT NOT NULL,
    size_units BIGINT NOT NULL,
    maker_amount TEXT,
    taker_amount TEXT,
    price NUMERIC(10,6) CHECK (price >= 0 AND price <= 1),
    size NUMERIC(20,6) CHECK (size > 0),
    filled_size NUMERIC(20,6) DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    signature TEXT,
    strategy_name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    filled_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_orders_wallet_status ON orders(wallet, status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at);
