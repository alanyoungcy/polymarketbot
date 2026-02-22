-- 004_trades.sql
-- Trade fills from all venues (Polymarket, Kalshi, Goldsky on-chain)

CREATE TABLE public.trades (
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

-- Idempotency: prevents duplicate ingestion from any source
CREATE UNIQUE INDEX idx_trades_source_idempotency
    ON public.trades(source, source_trade_id, COALESCE(source_log_idx, -1));

CREATE INDEX idx_trades_market_ts  ON public.trades(market_id, "timestamp");
CREATE INDEX idx_trades_maker      ON public.trades(maker);
CREATE INDEX idx_trades_taker      ON public.trades(taker);
CREATE INDEX idx_trades_timestamp  ON public.trades("timestamp");

ALTER TABLE public.trades ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.trades
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.trades
    FOR SELECT TO anon USING (true);
