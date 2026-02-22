-- Condition groups (multi-outcome events) and junction table.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE IF NOT EXISTS condition_groups (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active','closed','settled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_condition_groups_status ON condition_groups(status);

DROP TRIGGER IF EXISTS trg_condition_groups_updated_at ON condition_groups;
CREATE TRIGGER trg_condition_groups_updated_at
  BEFORE UPDATE ON condition_groups
  FOR EACH ROW EXECUTE PROCEDURE set_updated_at();

CREATE TABLE IF NOT EXISTS condition_group_markets (
  group_id  TEXT NOT NULL REFERENCES condition_groups(id) ON DELETE CASCADE,
  market_id TEXT NOT NULL REFERENCES markets(id) ON DELETE CASCADE,
  PRIMARY KEY (group_id, market_id)
);

CREATE INDEX IF NOT EXISTS idx_condition_group_markets_market ON condition_group_markets(market_id);
