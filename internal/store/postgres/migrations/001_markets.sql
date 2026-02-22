CREATE TABLE IF NOT EXISTS markets (
    id TEXT PRIMARY KEY,
    question TEXT NOT NULL,
    slug TEXT UNIQUE,
    outcome_1 TEXT NOT NULL,
    outcome_2 TEXT NOT NULL,
    token_id_1 TEXT NOT NULL,
    token_id_2 TEXT NOT NULL,
    condition_id TEXT,
    neg_risk BOOLEAN DEFAULT FALSE,
    volume NUMERIC(20,2) DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active',
    closed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_markets_token1 ON markets(token_id_1);
CREATE INDEX IF NOT EXISTS idx_markets_token2 ON markets(token_id_2);
CREATE INDEX IF NOT EXISTS idx_markets_status ON markets(status);
