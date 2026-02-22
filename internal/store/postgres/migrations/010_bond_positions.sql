-- Bond strategy positions (high-probability YES holdings to resolution).
CREATE TABLE IF NOT EXISTS bond_positions (
  id              TEXT PRIMARY KEY,
  market_id       TEXT NOT NULL REFERENCES markets(id) ON DELETE CASCADE,
  token_id        TEXT NOT NULL,
  entry_price     NUMERIC(10,6) NOT NULL CHECK (entry_price > 0 AND entry_price <= 1),
  expected_expiry  TIMESTAMPTZ NOT NULL,
  expected_apr     NUMERIC(10,4) NOT NULL,
  size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
  status          TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open','resolved_win','resolved_loss')),
  realized_pnl     NUMERIC(20,6) DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_bond_positions_status ON bond_positions(status);
CREATE INDEX IF NOT EXISTS idx_bond_positions_market ON bond_positions(market_id);
CREATE INDEX IF NOT EXISTS idx_bond_positions_expiry ON bond_positions(expected_expiry) WHERE status = 'open';
