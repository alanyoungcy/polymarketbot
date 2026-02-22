-- 003_positions.sql
-- Open and closed trading positions

CREATE TABLE public.positions (
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

CREATE INDEX idx_positions_wallet_status ON public.positions(wallet, status);
CREATE INDEX idx_positions_market        ON public.positions(market_id);
CREATE INDEX idx_positions_strategy      ON public.positions(strategy_name) WHERE strategy_name IS NOT NULL;
CREATE INDEX idx_positions_open          ON public.positions(status) WHERE status = 'open';

ALTER TABLE public.positions ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.positions
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.positions
    FOR SELECT TO anon USING (true);

ALTER PUBLICATION supabase_realtime ADD TABLE public.positions;
