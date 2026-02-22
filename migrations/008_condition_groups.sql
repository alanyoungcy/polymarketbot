-- 008_condition_groups.sql
-- Multi-outcome event groups (wraps N binary markets sharing one event)
-- e.g., "2024 US Presidential Election Winner" with 10+ candidate markets

CREATE TABLE public.condition_groups (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','closed','settled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trg_condition_groups_updated_at
    BEFORE UPDATE ON public.condition_groups
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE INDEX idx_cg_status ON public.condition_groups(status);

-- Junction table: many-to-many between groups and markets
CREATE TABLE public.condition_group_markets (
    group_id        TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    market_id       TEXT NOT NULL REFERENCES public.markets(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, market_id)
);

CREATE INDEX idx_cgm_market ON public.condition_group_markets(market_id);

-- RLS
ALTER TABLE public.condition_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.condition_group_markets ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.condition_groups
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.condition_groups
    FOR SELECT TO anon USING (true);

CREATE POLICY "service_role_all" ON public.condition_group_markets
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.condition_group_markets
    FOR SELECT TO anon USING (true);

-- Helper view: condition groups with market count
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
