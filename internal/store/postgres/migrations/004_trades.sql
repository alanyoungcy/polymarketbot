CREATE TABLE IF NOT EXISTS trades (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_trade_id TEXT NOT NULL,
    source_log_idx BIGINT,
    timestamp TIMESTAMPTZ NOT NULL,
    market_id TEXT NOT NULL REFERENCES markets(id),
    maker TEXT NOT NULL,
    taker TEXT NOT NULL,
    token_side TEXT NOT NULL,
    maker_direction TEXT NOT NULL,
    taker_direction TEXT NOT NULL,
    price NUMERIC(10,6) NOT NULL,
    usd_amount NUMERIC(20,6) NOT NULL,
    token_amount NUMERIC(20,6) NOT NULL,
    tx_hash TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trades_source_dedup
    ON trades(source, source_trade_id, COALESCE(source_log_idx, -1));
CREATE INDEX IF NOT EXISTS idx_trades_market_ts ON trades(market_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_trades_maker ON trades(maker);
CREATE INDEX IF NOT EXISTS idx_trades_taker ON trades(taker);
CREATE INDEX IF NOT EXISTS idx_trades_timestamp ON trades(timestamp);
