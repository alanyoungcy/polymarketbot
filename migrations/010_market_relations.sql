-- 010_market_relations.sql
-- Relationships between condition groups (for combinatorial arbitrage)

CREATE TABLE public.market_relations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_group_id TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    target_group_id TEXT NOT NULL REFERENCES public.condition_groups(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL
                    CHECK (relation_type IN ('implies','excludes','subset')),
    confidence      NUMERIC(5,4) DEFAULT 1.0
                    CHECK (confidence >= 0 AND confidence <= 1),
    config          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Prevent duplicate relations
    UNIQUE (source_group_id, target_group_id, relation_type),

    -- No self-relations
    CHECK (source_group_id <> target_group_id)
);

CREATE INDEX idx_mr_source ON public.market_relations(source_group_id);
CREATE INDEX idx_mr_target ON public.market_relations(target_group_id);

-- RLS
ALTER TABLE public.market_relations ENABLE ROW LEVEL SECURITY;

CREATE POLICY "service_role_all" ON public.market_relations
    FOR ALL TO service_role USING (true) WITH CHECK (true);

CREATE POLICY "anon_read" ON public.market_relations
    FOR SELECT TO anon USING (true);
