CREATE TABLE processing_jobs (
    processing_job_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type        TEXT NOT NULL CHECK (job_type IN ('PROCESS_DOCUMENT','VERIFY_INVOICE')),
    document_id     UUID REFERENCES documents(document_id) ON DELETE CASCADE,
    invoice_id      UUID,
    job_status      TEXT NOT NULL DEFAULT 'QUEUED' CHECK (job_status IN ('QUEUED','RUNNING','DONE','FAILED')),
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 3,
    run_after       TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at       TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX processing_jobs_ready_idx ON processing_jobs (run_after) WHERE job_status = 'QUEUED';
INSERT INTO processing_jobs (job_type, invoice_id) SELECT 'VERIFY_INVOICE', invoice_id FROM invoices WHERE verify_requested_at IS NOT NULL;
DROP INDEX IF EXISTS invoices_verify_requested_idx;
ALTER TABLE invoices DROP COLUMN IF EXISTS verify_requested_at;
