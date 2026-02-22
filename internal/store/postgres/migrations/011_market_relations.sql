-- Relationships between condition groups (combinatorial arb).
CREATE TABLE IF NOT EXISTS market_relations (
  id               TEXT PRIMARY KEY,
  source_group_id  TEXT NOT NULL REFERENCES condition_groups(id) ON DELETE CASCADE,
  target_group_id  TEXT NOT NULL REFERENCES condition_groups(id) ON DELETE CASCADE,
  relation_type    TEXT NOT NULL CHECK (relation_type IN ('implies','excludes','subset')),
  confidence       NUMERIC(5,4) DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
  config           JSONB,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (source_group_id, target_group_id, relation_type),
  CHECK (source_group_id <> target_group_id)
);

CREATE INDEX IF NOT EXISTS idx_market_relations_source ON market_relations(source_group_id);
CREATE INDEX IF NOT EXISTS idx_market_relations_target ON market_relations(target_group_id);
