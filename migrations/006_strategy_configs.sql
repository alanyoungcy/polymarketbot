-- 006_strategy_configs.sql
-- Per-strategy configuration (flexible JSONB)

CREATE TABLE public.strategy_configs (
    name            TEXT PRIMARY KEY,
    config_json     JSONB NOT NULL,
    enabled         BOOLEAN DEFAULT TRUE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trg_strategy_configs_updated_at
    BEFORE UPDATE ON public.strategy_configs
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

ALTER TABLE public.strategy_configs ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.strategy_configs
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.strategy_configs
    FOR SELECT TO anon USING (true);

-- Seed default strategy configs
INSERT INTO public.strategy_configs (name, config_json, enabled) VALUES
    ('flash_crash', '{"drop_threshold": 0.10, "recovery_target": 0.05}', true),
    ('mean_reversion', '{"std_dev_threshold": 2.0, "lookback_window": "5m"}', true),
    ('arb', '{}', false),
    ('rebalancing_arb', '{"min_edge_bps": 50, "max_group_size": 10, "size_per_leg": 5.0, "ttl_seconds": 30, "max_stale_sec": 5}', false),
    ('bond', '{"min_yes_price": 0.95, "min_apr": 0.10, "min_volume": 100000, "max_days_to_exp": 90, "min_days_to_exp": 7, "max_positions": 10, "size_per_position": 50.0}', false),
    ('liquidity_provider', '{"half_spread_bps": 50, "requote_threshold": 0.005, "size": 10.0, "max_markets": 5, "min_volume": 50000, "rewards_only": true}', false),
    ('combinatorial_arb', '{"min_edge_bps": 100, "max_relations": 10, "size_per_leg": 5.0}', false)
ON CONFLICT (name) DO NOTHING;
