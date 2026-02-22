-- 009_bond_positions.sql
-- High-probability bond strategy: tracks YES token holdings to resolution

CREATE TABLE public.bond_positions (
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

CREATE INDEX idx_bond_status ON public.bond_positions(status);
CREATE INDEX idx_bond_market ON public.bond_positions(market_id);
CREATE INDEX idx_bond_expiry ON public.bond_positions(expected_expiry)
    WHERE status = 'open';

-- RLS
ALTER TABLE public.bond_positions ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.bond_positions
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.bond_positions
    FOR SELECT TO anon USING (true);

ALTER PUBLICATION supabase_realtime ADD TABLE public.bond_positions;

-- Helper view: open bond portfolio summary
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
    (1.0 - bp.entry_price) * bp.size AS expected_yield,
    bp.created_at
FROM public.bond_positions bp
JOIN public.markets m ON m.id = bp.market_id
WHERE bp.status = 'open'
ORDER BY bp.expected_expiry ASC;
