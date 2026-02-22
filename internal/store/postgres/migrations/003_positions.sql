CREATE TABLE IF NOT EXISTS positions (
    id TEXT PRIMARY KEY,
    market_id TEXT NOT NULL REFERENCES markets(id),
    token_id TEXT NOT NULL,
    wallet TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('token1', 'token2')),
    direction TEXT NOT NULL CHECK (direction IN ('buy', 'sell')),
    entry_price NUMERIC(10,6) NOT NULL,
    size NUMERIC(20,6) NOT NULL,
    take_profit NUMERIC(10,6),
    stop_loss NUMERIC(10,6),
    realized_pnl NUMERIC(20,6) DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    strategy_name TEXT,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    exit_price NUMERIC(10,6),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_positions_wallet_status ON positions(wallet, status);
