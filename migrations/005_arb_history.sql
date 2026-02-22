-- 005_arb_history.sql
-- Detected cross-platform arbitrage opportunities

CREATE TABLE public.arb_history (
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
                        CHECK (direction IN ('poly_yes_kalshi_no', 'poly_no_kalshi_yes')),
    max_amount          NUMERIC(20,6) CHECK (max_amount IS NULL OR max_amount > 0),
    detected_at         TIMESTAMPTZ NOT NULL,
    duration_ms         BIGINT,
    executed            BOOLEAN DEFAULT FALSE,
    executed_at         TIMESTAMPTZ
);

CREATE INDEX idx_arb_detected  ON public.arb_history(detected_at);
CREATE INDEX idx_arb_net_edge  ON public.arb_history(net_edge_bps);
CREATE INDEX idx_arb_executed  ON public.arb_history(executed) WHERE executed = TRUE;

ALTER TABLE public.arb_history ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.arb_history
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.arb_history
    FOR SELECT TO anon USING (true);
