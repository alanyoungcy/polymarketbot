-- Allow single-venue arbitrage direction values (spread, imbalance, etc.).
ALTER TABLE arb_history DROP CONSTRAINT IF EXISTS arb_history_direction_check;
ALTER TABLE arb_history ADD CONSTRAINT arb_history_direction_check
  CHECK (direction IS NOT NULL AND length(trim(direction)) > 0);
