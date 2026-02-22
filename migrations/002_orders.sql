-- 002_orders.sql
-- Order history from CLOB API submissions

CREATE TABLE public.orders (
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

CREATE INDEX idx_orders_wallet_status ON public.orders(wallet, status);
CREATE INDEX idx_orders_market        ON public.orders(market_id);
CREATE INDEX idx_orders_created       ON public.orders(created_at);
CREATE INDEX idx_orders_strategy      ON public.orders(strategy_name) WHERE strategy_name IS NOT NULL;

ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.orders
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.orders
    FOR SELECT TO anon USING (true);

ALTER PUBLICATION supabase_realtime ADD TABLE public.orders;
