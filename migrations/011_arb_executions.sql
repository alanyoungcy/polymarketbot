-- 011_arb_executions.sql
-- Arbitrage execution tracking with per-leg PnL
-- Tracks realized profit for rebalancing, combinatorial, and cross-platform arb

CREATE TABLE public.arb_executions (
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

CREATE INDEX idx_arb_exec_type    ON public.arb_executions(arb_type);
CREATE INDEX idx_arb_exec_started ON public.arb_executions(started_at);
CREATE INDEX idx_arb_exec_status  ON public.arb_executions(status);
CREATE INDEX idx_arb_exec_pnl     ON public.arb_executions(net_pnl_usd);

-- Individual legs within an arb execution
CREATE TABLE public.arb_execution_legs (
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

CREATE INDEX idx_arb_leg_exec ON public.arb_execution_legs(execution_id);

-- RLS
ALTER TABLE public.arb_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.arb_execution_legs ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.arb_executions
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.arb_executions
    FOR SELECT TO anon USING (true);

CREATE POLICY "service_role_all" ON public.arb_execution_legs
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.arb_execution_legs
    FOR SELECT TO anon USING (true);

ALTER PUBLICATION supabase_realtime ADD TABLE public.arb_executions;

-- ============================================================
-- PROFIT REPORTING VIEWS
-- ============================================================

-- Arb profit summary by type
CREATE OR REPLACE VIEW public.arb_profit_summary AS
SELECT
    arb_type,
    COUNT(*)                                          AS total_executions,
    COUNT(*) FILTER (WHERE net_pnl_usd > 0)           AS wins,
    COUNT(*) FILTER (WHERE net_pnl_usd <= 0)          AS losses,
    COALESCE(SUM(net_pnl_usd), 0)                    AS total_pnl_usd,
    COALESCE(AVG(net_pnl_usd), 0)                    AS avg_pnl_usd,
    COALESCE(SUM(total_fees), 0)                      AS total_fees_usd,
    COALESCE(SUM(total_slippage), 0)                  AS total_slippage_usd,
    COALESCE(AVG(gross_edge_bps), 0)                  AS avg_gross_edge_bps,
    MIN(started_at)                                    AS first_execution,
    MAX(started_at)                                    AS last_execution
FROM public.arb_executions
WHERE status = 'filled'
GROUP BY arb_type;

-- Arb profit over time (daily buckets)
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

-- Recent arb executions with leg details
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
            'leg_id', ael.id,
            'market_id', ael.market_id,
            'side', ael.side,
            'expected_price', ael.expected_price,
            'filled_price', ael.filled_price,
            'size', ael.size,
            'fee_usd', ael.fee_usd,
            'slippage_bps', ael.slippage_bps,
            'status', ael.status
        ) ORDER BY ael.id
    ) AS legs
FROM public.arb_executions ae
LEFT JOIN public.arb_execution_legs ael ON ael.execution_id = ae.id
GROUP BY ae.id, ae.arb_type, ae.leg_group_id, ae.gross_edge_bps,
         ae.net_pnl_usd, ae.total_fees, ae.total_slippage,
         ae.status, ae.started_at, ae.completed_at;

-- ============================================================
-- SUPABASE DATABASE FUNCTIONS (for pg_notify / RPC)
-- ============================================================

-- RPC: get session arb PnL since a given time
CREATE OR REPLACE FUNCTION public.get_arb_pnl(since_ts TIMESTAMPTZ DEFAULT NOW() - INTERVAL '24 hours')
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
        COALESCE(SUM(ae.net_pnl_usd), 0)                  AS total_pnl,
        COUNT(*)                                            AS execution_count,
        COUNT(*) FILTER (WHERE ae.net_pnl_usd > 0)         AS win_count,
        COUNT(*) FILTER (WHERE ae.net_pnl_usd <= 0)        AS loss_count
    FROM public.arb_executions ae
    WHERE ae.status = 'filled'
      AND ae.started_at >= since_ts
    GROUP BY ae.arb_type;
END;
$$ LANGUAGE plpgsql STABLE;

-- RPC: get bond portfolio summary
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
        COUNT(*)                                                              AS open_positions,
        COALESCE(SUM(bp.entry_price * bp.size), 0)                           AS total_invested,
        COALESCE(SUM((1.0 - bp.entry_price) * bp.size), 0)                  AS expected_yield,
        CASE
            WHEN SUM(bp.size) > 0 THEN
                SUM(bp.expected_apr * bp.size) / SUM(bp.size)
            ELSE 0
        END                                                                   AS weighted_apr,
        COALESCE(AVG(EXTRACT(DAY FROM bp.expected_expiry - NOW())), 0)       AS avg_days_to_exp
    FROM public.bond_positions bp
    WHERE bp.status = 'open';
END;
$$ LANGUAGE plpgsql STABLE;
