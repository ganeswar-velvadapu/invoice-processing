UPDATE invoices SET invoice_status = 'REVIEW_REQUIRED' WHERE invoice_status = 'AWAITING_APPROVAL';
ALTER TABLE invoices DROP COLUMN IF EXISTS clerk_note, DROP COLUMN IF EXISTS clerk_recommendation,
  DROP COLUMN IF EXISTS submitted_at, DROP COLUMN IF EXISTS submitted_by_user_id;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_invoice_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_invoice_status_check CHECK (invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH',
  'PENDING_GRN','READY_FOR_VERIFICATION','REVIEW_REQUIRED','ON_HOLD','RETURNED','APPROVED','REJECTED','PAYMENT_PENDING','PAID'));
UPDATE invoice_exceptions SET exception_status = 'OPEN' WHERE exception_status = 'CHECKED';
ALTER TABLE invoice_exceptions DROP CONSTRAINT IF EXISTS invoice_exceptions_exception_status_check;
ALTER TABLE invoice_exceptions ADD CONSTRAINT invoice_exceptions_exception_status_check
  CHECK (exception_status IN ('OPEN','ASSIGNED','RESOLVED','WAIVED'));
