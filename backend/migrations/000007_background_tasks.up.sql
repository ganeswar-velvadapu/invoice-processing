-- Background work now runs in goroutines inside the API. The database still records what is
-- unfinished: documents keep a pending status, and invoices that need a re-check are flagged here.
ALTER TABLE invoices ADD COLUMN verify_requested_at TIMESTAMPTZ;
UPDATE invoices SET verify_requested_at = now()
  WHERE invoice_id IN (SELECT invoice_id FROM processing_jobs
                       WHERE job_type = 'VERIFY_INVOICE' AND job_status IN ('QUEUED','RUNNING'));
CREATE INDEX invoices_verify_requested_idx ON invoices (verify_requested_at) WHERE verify_requested_at IS NOT NULL;
DROP TABLE processing_jobs;
