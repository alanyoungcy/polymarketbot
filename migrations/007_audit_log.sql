-- 007_audit_log.sql
-- Immutable event/audit log

CREATE TABLE public.audit_log (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event           TEXT NOT NULL,
    detail          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_event ON public.audit_log(event, created_at);

ALTER TABLE public.audit_log ENABLE ROW LEVEL SECURITY;

-- Audit log: service_role can insert + read; no updates or deletes (immutable)
CREATE POLICY "service_role_insert" ON public.audit_log
    FOR INSERT TO service_role WITH CHECK (true);

CREATE POLICY "service_role_select" ON public.audit_log
    FOR SELECT TO service_role USING (true);

CREATE POLICY "anon_read" ON public.audit_log
    FOR SELECT TO anon USING (true);
