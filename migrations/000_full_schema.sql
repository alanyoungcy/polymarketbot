-- ============================================================
-- Polymarket Bot — Complete Supabase Schema (idempotent)
-- Safe to re-run: uses IF NOT EXISTS, DO blocks, CREATE OR REPLACE
-- Paste into Supabase SQL Editor and run
-- ============================================================

-- Helper: auto-update updated_at on row modification
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;


-- ============================================================
-- 001: MARKETS
-- ============================================================

CREATE TABLE IF NOT EXISTS public.markets (
    id              TEXT PRIMARY KEY,
    question        TEXT NOT NULL,
    slug            TEXT UNIQUE,
    outcome_1       TEXT NOT NULL,
    outcome_2       TEXT NOT NULL,
    token_id_1      TEXT NOT NULL,
    token_id_2      TEXT NOT NULL,
    condition_id    TEXT,
    neg_risk        BOOLEAN DEFAULT FALSE,
    volume          NUMERIC(20,2) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','closed','settled')),
    closed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_markets_token1  ON public.markets(token_id_1);
CREATE INDEX IF NOT EXISTS idx_markets_token2  ON public.markets(token_id_2);
CREATE INDEX IF NOT EXISTS idx_markets_slug    ON public.markets(slug);
CREATE INDEX IF NOT EXISTS idx_markets_status  ON public.markets(status);

DROP TRIGGER IF EXISTS trg_markets_updated_at ON public.markets;
CREATE TRIGGER trg_markets_updated_at
    BEFORE UPDATE ON public.markets
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

ALTER TABLE public.markets ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.markets FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.markets FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER PUBLICATION supabase_realtime ADD TABLE public.markets;


-- ============================================================
-- 002: ORDERS
-- ============================================================

CREATE TABLE IF NOT EXISTS public.orders (
    id              TEXT PRIMARY KEY,
    market_id       TEXT NOT NULL REFERENCES public.markets(id) ON DELETE CASCADE,
    token_id        TEXT NOT NULL,
    wallet          TEXT NOT NULL,
    side            TEXT NOT NULL CHECK (side IN ('buy','sell')),
    order_type      TEXT NOT NULL CHECK (order_type IN ('GTC','GTD','FOK','FAK')),
    price_ticks     BIGINT NOT NULL,
    size_units      BIGINT NOT NULL,
    price           NUMERIC(10,6) NOT NULL CHECK (price >= 0 AND price <= 1),
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    filled_size     NUMERIC(20,6) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','open','matched','cancelled','failed')),
    signature       TEXT,
    strategy_name   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    filled_at       TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_orders_wallet_status ON public.orders(wallet, status);
CREATE INDEX IF NOT EXISTS idx_orders_market        ON public.orders(market_id);
CREATE INDEX IF NOT EXISTS idx_orders_created       ON public.orders(created_at);
CREATE INDEX IF NOT EXISTS idx_orders_strategy      ON public.orders(strategy_name) WHERE strategy_name IS NOT NULL;

ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.orders FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.orders FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER PUBLICATION supabase_realtime ADD TABLE public.orders;


-- ============================================================
-- 003: POSITIONS
-- ============================================================

CREATE TABLE IF NOT EXISTS public.positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id       TEXT NOT NULL REFERENCES public.markets(id) ON DELETE CASCADE,
    token_id        TEXT NOT NULL,
    wallet          TEXT NOT NULL,
    side            TEXT NOT NULL CHECK (side IN ('token1','token2')),
    direction       TEXT NOT NULL CHECK (direction IN ('buy','sell')),
    entry_price     NUMERIC(10,6) NOT NULL CHECK (entry_price >= 0 AND entry_price <= 1),
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    take_profit     NUMERIC(10,6),
    stop_loss       NUMERIC(10,6),
    realized_pnl    NUMERIC(20,6) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','closed')),
    strategy_name   TEXT,
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at       TIMESTAMPTZ,
    exit_price      NUMERIC(10,6)
);

CREATE INDEX IF NOT EXISTS idx_positions_wallet_status ON public.positions(wallet, status);
CREATE INDEX IF NOT EXISTS idx_positions_market        ON public.positions(market_id);
CREATE INDEX IF NOT EXISTS idx_positions_strategy      ON public.positions(strategy_name) WHERE strategy_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_positions_open          ON public.positions(status) WHERE status = 'open';

ALTER TABLE public.positions ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.positions FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.positions FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER PUBLICATION supabase_realtime ADD TABLE public.positions;


-- ============================================================
-- 004: TRADES
-- ============================================================

CREATE TABLE IF NOT EXISTS public.trades (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source          TEXT NOT NULL,
    source_trade_id TEXT NOT NULL,
    source_log_idx  BIGINT,
    "timestamp"     TIMESTAMPTZ NOT NULL,
    market_id       TEXT REFERENCES public.markets(id) ON DELETE SET NULL,
    maker           TEXT NOT NULL,
    taker           TEXT NOT NULL,
    token_side      TEXT NOT NULL CHECK (token_side IN ('token1','token2')),
    maker_direction TEXT NOT NULL CHECK (maker_direction IN ('buy','sell')),
    taker_direction TEXT NOT NULL CHECK (taker_direction IN ('buy','sell')),
    price           NUMERIC(10,6) NOT NULL CHECK (price >= 0 AND price <= 1),
    usd_amount      NUMERIC(20,6) NOT NULL CHECK (usd_amount >= 0),
    token_amount    NUMERIC(20,6) NOT NULL CHECK (token_amount > 0),
    tx_hash         TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trades_source_idempotency
    ON public.trades(source, source_trade_id, COALESCE(source_log_idx, -1));
CREATE INDEX IF NOT EXISTS idx_trades_market_ts  ON public.trades(market_id, "timestamp");
CREATE INDEX IF NOT EXISTS idx_trades_maker      ON public.trades(maker);
CREATE INDEX IF NOT EXISTS idx_trades_taker      ON public.trades(taker);
CREATE INDEX IF NOT EXISTS idx_trades_timestamp  ON public.trades("timestamp");

ALTER TABLE public.trades ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.trades FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.trades FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;


-- ============================================================
-- 005: ARB HISTORY
-- ============================================================

CREATE TABLE IF NOT EXISTS public.arb_history (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    poly_market_id      TEXT,
    poly_token_id       TEXT,
    poly_price          NUMERIC(10,6) NOT NULL,
    kalshi_market_id    TEXT,
    kalshi_price        NUMERIC(10,6) NOT NULL,
    gross_edge_bps      NUMERIC(10,2) NOT NULL,
    est_fee_bps         NUMERIC(10,2) NOT NULL,
    est_slippage_bps    NUMERIC(10,2) NOT NULL,
    est_latency_bps     NUMERIC(10,2) NOT NULL,
    net_edge_bps        NUMERIC(10,2) NOT NULL,
    expected_pnl_usd    NUMERIC(20,6) NOT NULL,
    direction           TEXT NOT NULL
                        CHECK (direction IN ('poly_yes_kalshi_no','poly_no_kalshi_yes')),
    max_amount          NUMERIC(20,6) CHECK (max_amount IS NULL OR max_amount > 0),
    detected_at         TIMESTAMPTZ NOT NULL,
    duration_ms         BIGINT,
    executed            BOOLEAN DEFAULT FALSE,
    executed_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_arb_detected  ON public.arb_history(detected_at);
CREATE INDEX IF NOT EXISTS idx_arb_net_edge  ON public.arb_history(net_edge_bps);
CREATE INDEX IF NOT EXISTS idx_arb_executed  ON public.arb_history(executed) WHERE executed = TRUE;

ALTER TABLE public.arb_history ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.arb_history FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.arb_history FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;


-- ============================================================
-- 006: STRATEGY CONFIGS
-- ============================================================

CREATE TABLE IF NOT EXISTS public.strategy_configs (
    name            TEXT PRIMARY KEY,
    config_json     JSONB NOT NULL,
    enabled         BOOLEAN DEFAULT TRUE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_strategy_configs_updated_at ON public.strategy_configs;
CREATE TRIGGER trg_strategy_configs_updated_at
    BEFORE UPDATE ON public.strategy_configs
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

ALTER TABLE public.strategy_configs ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.strategy_configs FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.strategy_configs FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

INSERT INTO public.strategy_configs (name, config_json, enabled) VALUES
    ('flash_crash',       '{"drop_threshold": 0.10, "recovery_target": 0.05}', true),
    ('mean_reversion',    '{"std_dev_threshold": 2.0, "lookback_window": "5m"}', true),
    ('arb',               '{}', false),
    ('rebalancing_arb',   '{"min_edge_bps": 50, "max_group_size": 10, "size_per_leg": 5.0, "ttl_seconds": 30, "max_stale_sec": 5}', false),
    ('bond',              '{"min_yes_price": 0.95, "min_apr": 0.10, "min_volume": 100000, "max_days_to_exp": 90, "min_days_to_exp": 7, "max_positions": 10, "size_per_position": 50.0}', false),
    ('liquidity_provider','{"half_spread_bps": 50, "requote_threshold": 0.005, "size": 10.0, "max_markets": 5, "min_volume": 50000, "rewards_only": true}', false),
    ('combinatorial_arb', '{"min_edge_bps": 100, "max_relations": 10, "size_per_leg": 5.0}', false)
ON CONFLICT (name) DO NOTHING;


-- ============================================================
-- 007: AUDIT LOG
-- ============================================================

CREATE TABLE IF NOT EXISTS public.audit_log (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event           TEXT NOT NULL,
    detail          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_event ON public.audit_log(event, created_at);

ALTER TABLE public.audit_log ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_insert" ON public.audit_log FOR INSERT TO service_role WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "service_role_select" ON public.audit_log FOR SELECT TO service_role USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.audit_log FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;


-- ============================================================
-- 008: CONDITION GROUPS (multi-outcome events)
-- ============================================================

CREATE TABLE IF NOT EXISTS public.condition_groups (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','closed','settled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cg_status ON public.condition_groups(status);

DROP TRIGGER IF EXISTS trg_condition_groups_updated_at ON public.condition_groups;
CREATE TRIGGER trg_condition_groups_updated_at
    BEFORE UPDATE ON public.condition_groups
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE IF NOT EXISTS public.condition_group_markets (
    group_id        TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    market_id       TEXT NOT NULL REFERENCES public.markets(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, market_id)
);

CREATE INDEX IF NOT EXISTS idx_cgm_market ON public.condition_group_markets(market_id);

ALTER TABLE public.condition_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.condition_group_markets ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.condition_groups FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.condition_groups FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.condition_group_markets FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.condition_group_markets FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE OR REPLACE VIEW public.condition_groups_summary AS
SELECT
    cg.id,
    cg.title,
    cg.status,
    COUNT(cgm.market_id) AS market_count,
    cg.created_at,
    cg.updated_at
FROM public.condition_groups cg
LEFT JOIN public.condition_group_markets cgm ON cg.id = cgm.group_id
GROUP BY cg.id, cg.title, cg.status, cg.created_at, cg.updated_at;


-- ============================================================
-- 009: BOND POSITIONS
-- ============================================================

CREATE TABLE IF NOT EXISTS public.bond_positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id       TEXT NOT NULL REFERENCES public.markets(id) ON DELETE CASCADE,
    token_id        TEXT NOT NULL,
    entry_price     NUMERIC(10,6) NOT NULL
                    CHECK (entry_price > 0 AND entry_price <= 1),
    expected_expiry TIMESTAMPTZ NOT NULL,
    expected_apr    NUMERIC(10,4) NOT NULL,
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    status          TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','resolved_win','resolved_loss')),
    realized_pnl    NUMERIC(20,6) DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_bond_status ON public.bond_positions(status);
CREATE INDEX IF NOT EXISTS idx_bond_market ON public.bond_positions(market_id);
CREATE INDEX IF NOT EXISTS idx_bond_expiry ON public.bond_positions(expected_expiry) WHERE status = 'open';

ALTER TABLE public.bond_positions ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.bond_positions FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.bond_positions FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER PUBLICATION supabase_realtime ADD TABLE public.bond_positions;

CREATE OR REPLACE VIEW public.bond_portfolio AS
SELECT
    bp.id,
    bp.market_id,
    m.question,
    bp.entry_price,
    bp.expected_apr,
    bp.size,
    bp.expected_expiry,
    EXTRACT(DAY FROM bp.expected_expiry - NOW())::INT AS days_remaining,
    (1.0 - bp.entry_price) * bp.size                  AS expected_yield,
    bp.created_at
FROM public.bond_positions bp
JOIN public.markets m ON m.id = bp.market_id
WHERE bp.status = 'open'
ORDER BY bp.expected_expiry ASC;


-- ============================================================
-- 010: MARKET RELATIONS (combinatorial arb)
-- ============================================================

CREATE TABLE IF NOT EXISTS public.market_relations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_group_id TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    target_group_id TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL
                    CHECK (relation_type IN ('implies','excludes','subset')),
    confidence      NUMERIC(5,4) DEFAULT 1.0
                    CHECK (confidence >= 0 AND confidence <= 1),
    config          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_group_id, target_group_id, relation_type),
    CHECK (source_group_id <> target_group_id)
);

CREATE INDEX IF NOT EXISTS idx_mr_source ON public.market_relations(source_group_id);
CREATE INDEX IF NOT EXISTS idx_mr_target ON public.market_relations(target_group_id);

ALTER TABLE public.market_relations ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.market_relations FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.market_relations FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;


-- ============================================================
-- 011: ARB EXECUTIONS + LEGS (profit tracking)
-- ============================================================

CREATE TABLE IF NOT EXISTS public.arb_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    opportunity_id  UUID REFERENCES public.arb_history(id) ON DELETE SET NULL,
    arb_type        TEXT NOT NULL
                    CHECK (arb_type IN ('rebalancing','combinatorial','cross_platform')),
    leg_group_id    TEXT NOT NULL,
    gross_edge_bps  NUMERIC(10,2),
    total_fees      NUMERIC(20,6) DEFAULT 0,
    total_slippage  NUMERIC(20,6) DEFAULT 0,
    net_pnl_usd     NUMERIC(20,6) NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','partial','filled','cancelled','failed')),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_arb_exec_type    ON public.arb_executions(arb_type);
CREATE INDEX IF NOT EXISTS idx_arb_exec_started ON public.arb_executions(started_at);
CREATE INDEX IF NOT EXISTS idx_arb_exec_status  ON public.arb_executions(status);
CREATE INDEX IF NOT EXISTS idx_arb_exec_pnl     ON public.arb_executions(net_pnl_usd);

CREATE TABLE IF NOT EXISTS public.arb_execution_legs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execution_id    UUID NOT NULL REFERENCES public.arb_executions(id) ON DELETE CASCADE,
    order_id        TEXT,
    market_id       TEXT REFERENCES public.markets(id) ON DELETE SET NULL,
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

CREATE INDEX IF NOT EXISTS idx_arb_leg_exec ON public.arb_execution_legs(execution_id);

ALTER TABLE public.arb_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.arb_execution_legs ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.arb_executions FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.arb_executions FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "service_role_all" ON public.arb_execution_legs FOR ALL TO service_role USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY "anon_read" ON public.arb_execution_legs FOR SELECT TO anon USING (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER PUBLICATION supabase_realtime ADD TABLE public.arb_executions;


-- ============================================================
-- VIEWS: Arb Profit Reporting
-- ============================================================

CREATE OR REPLACE VIEW public.arb_profit_summary AS
SELECT
    arb_type,
    COUNT(*)                                     AS total_executions,
    COUNT(*) FILTER (WHERE net_pnl_usd > 0)      AS wins,
    COUNT(*) FILTER (WHERE net_pnl_usd <= 0)     AS losses,
    COALESCE(SUM(net_pnl_usd), 0)               AS total_pnl_usd,
    COALESCE(AVG(net_pnl_usd), 0)               AS avg_pnl_usd,
    COALESCE(SUM(total_fees), 0)                 AS total_fees_usd,
    COALESCE(SUM(total_slippage), 0)             AS total_slippage_usd,
    COALESCE(AVG(gross_edge_bps), 0)             AS avg_gross_edge_bps,
    MIN(started_at)                               AS first_execution,
    MAX(started_at)                               AS last_execution
FROM public.arb_executions
WHERE status = 'filled'
GROUP BY arb_type;

CREATE OR REPLACE VIEW public.arb_profit_daily AS
SELECT
    DATE_TRUNC('day', started_at)::DATE AS day,
    arb_type,
    COUNT(*)                             AS executions,
    COALESCE(SUM(net_pnl_usd), 0)      AS pnl_usd,
    COALESCE(SUM(total_fees), 0)        AS fees_usd
FROM public.arb_executions
WHERE status = 'filled'
GROUP BY DATE_TRUNC('day', started_at), arb_type
ORDER BY day DESC;

CREATE OR REPLACE VIEW public.arb_executions_detail AS
SELECT
    ae.id AS execution_id,
    ae.arb_type,
    ae.leg_group_id,
    ae.gross_edge_bps,
    ae.net_pnl_usd,
    ae.total_fees,
    ae.total_slippage,
    ae.status,
    ae.started_at,
    ae.completed_at,
    JSONB_AGG(
        JSONB_BUILD_OBJECT(
            'leg_id',         ael.id,
            'market_id',      ael.market_id,
            'side',           ael.side,
            'expected_price', ael.expected_price,
            'filled_price',   ael.filled_price,
            'size',           ael.size,
            'fee_usd',        ael.fee_usd,
            'slippage_bps',   ael.slippage_bps,
            'status',         ael.status
        ) ORDER BY ael.id
    ) AS legs
FROM public.arb_executions ae
LEFT JOIN public.arb_execution_legs ael ON ael.execution_id = ae.id
GROUP BY ae.id, ae.arb_type, ae.leg_group_id, ae.gross_edge_bps,
         ae.net_pnl_usd, ae.total_fees, ae.total_slippage,
         ae.status, ae.started_at, ae.completed_at;


-- ============================================================
-- RPC FUNCTIONS (callable via supabase.rpc())
-- ============================================================

CREATE OR REPLACE FUNCTION public.get_arb_pnl(
    since_ts TIMESTAMPTZ DEFAULT NOW() - INTERVAL '24 hours'
)
RETURNS TABLE (
    arb_type        TEXT,
    total_pnl       NUMERIC,
    execution_count BIGINT,
    win_count       BIGINT,
    loss_count      BIGINT
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        ae.arb_type,
        COALESCE(SUM(ae.net_pnl_usd), 0)           AS total_pnl,
        COUNT(*)                                     AS execution_count,
        COUNT(*) FILTER (WHERE ae.net_pnl_usd > 0)  AS win_count,
        COUNT(*) FILTER (WHERE ae.net_pnl_usd <= 0) AS loss_count
    FROM public.arb_executions ae
    WHERE ae.status = 'filled'
      AND ae.started_at >= since_ts
    GROUP BY ae.arb_type;
END;
$$ LANGUAGE plpgsql STABLE;

CREATE OR REPLACE FUNCTION public.get_bond_portfolio_summary()
RETURNS TABLE (
    open_positions   BIGINT,
    total_invested   NUMERIC,
    expected_yield   NUMERIC,
    weighted_apr     NUMERIC,
    avg_days_to_exp  NUMERIC
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        COUNT(*)                                                         AS open_positions,
        COALESCE(SUM(bp.entry_price * bp.size), 0)                      AS total_invested,
        COALESCE(SUM((1.0 - bp.entry_price) * bp.size), 0)             AS expected_yield,
        CASE WHEN SUM(bp.size) > 0
             THEN SUM(bp.expected_apr * bp.size) / SUM(bp.size)
             ELSE 0
        END                                                              AS weighted_apr,
        COALESCE(AVG(EXTRACT(DAY FROM bp.expected_expiry - NOW())), 0)  AS avg_days_to_exp
    FROM public.bond_positions bp
    WHERE bp.status = 'open';
END;
$$ LANGUAGE plpgsql STABLE;
