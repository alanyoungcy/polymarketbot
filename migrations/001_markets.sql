-- 001_markets.sql
-- Polymarket binary market metadata
-- Supabase: RLS enabled, Realtime enabled

-- Helper: auto-update updated_at on row modification
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE public.markets (
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

CREATE INDEX idx_markets_token1  ON public.markets(token_id_1);
CREATE INDEX idx_markets_token2  ON public.markets(token_id_2);
CREATE INDEX idx_markets_slug    ON public.markets(slug);
CREATE INDEX idx_markets_status  ON public.markets(status);

CREATE TRIGGER trg_markets_updated_at
    BEFORE UPDATE ON public.markets
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- RLS: bot uses service_role key (bypasses RLS); anon gets read-only
ALTER TABLE public.markets ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.markets
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.markets
    FOR SELECT TO anon USING (true);

-- Supabase Realtime: enable publication for live market status updates
ALTER PUBLICATION supabase_realtime ADD TABLE public.markets;
