CREATE TABLE IF NOT EXISTS arb_history (
    id TEXT PRIMARY KEY,
    poly_market_id TEXT,
    poly_token_id TEXT,
    poly_price NUMERIC(10,6),
    kalshi_market_id TEXT,
    kalshi_price NUMERIC(10,6),
    gross_edge_bps NUMERIC(12,4),
    est_fee_bps NUMERIC(12,4),
    est_slippage_bps NUMERIC(12,4),
    est_latency_bps NUMERIC(12,4),
    net_edge_bps NUMERIC(12,4),
    expected_pnl_usd NUMERIC(20,6),
    direction TEXT CHECK (direction IN ('poly_yes_kalshi_no', 'poly_no_kalshi_yes')),
    max_amount NUMERIC(20,6),
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_ms BIGINT,
    executed BOOLEAN DEFAULT FALSE,
    executed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_arb_history_detected_at ON arb_history(detected_at);
CREATE INDEX IF NOT EXISTS idx_arb_history_net_edge_bps ON arb_history(net_edge_bps);
