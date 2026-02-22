-- Arbitrage execution tracking with per-leg PnL.
CREATE TABLE IF NOT EXISTS arb_executions (
  id             TEXT PRIMARY KEY,
  opportunity_id TEXT,
  arb_type       TEXT NOT NULL CHECK (arb_type IN ('rebalancing','combinatorial','cross_platform')),
  leg_group_id   TEXT NOT NULL,
  gross_edge_bps NUMERIC(10,2),
  total_fees     NUMERIC(20,6) DEFAULT 0,
  total_slippage NUMERIC(20,6) DEFAULT 0,
  net_pnl_usd    NUMERIC(20,6) NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','partial','filled','cancelled','failed')),
  started_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_arb_executions_type ON arb_executions(arb_type);
CREATE INDEX IF NOT EXISTS idx_arb_executions_started ON arb_executions(started_at);
CREATE INDEX IF NOT EXISTS idx_arb_executions_status ON arb_executions(status);

CREATE TABLE IF NOT EXISTS arb_execution_legs (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  execution_id    TEXT NOT NULL REFERENCES arb_executions(id) ON DELETE CASCADE,
  order_id        TEXT,
  market_id       TEXT REFERENCES markets(id) ON DELETE SET NULL,
  token_id        TEXT NOT NULL,
  side            TEXT NOT NULL CHECK (side IN ('buy','sell')),
  expected_price  NUMERIC(10,6) NOT NULL,
  filled_price    NUMERIC(10,6),
  size            NUMERIC(20,6) NOT NULL,
  fee_usd         NUMERIC(20,6) DEFAULT 0,
  slippage_bps    NUMERIC(10,2) DEFAULT 0,
  status          TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','filled','cancelled','failed'))
);

CREATE INDEX IF NOT EXISTS idx_arb_execution_legs_exec ON arb_execution_legs(execution_id);
