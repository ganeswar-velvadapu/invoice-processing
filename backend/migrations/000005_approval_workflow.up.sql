-- Clerk review workflow: exceptions can be marked CHECKED (looked at and explained, still failing),
-- and an invoice can be sent for manager approval with the clerk's recommendation.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_invoice_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_invoice_status_check CHECK (invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH',
  'PENDING_GRN','READY_FOR_VERIFICATION','REVIEW_REQUIRED','ON_HOLD','RETURNED','AWAITING_APPROVAL','APPROVED','REJECTED',
  'PAYMENT_PENDING','PAID'));
ALTER TABLE invoices ADD COLUMN submitted_by_user_id UUID REFERENCES users(user_id);
ALTER TABLE invoices ADD COLUMN submitted_at TIMESTAMPTZ;
ALTER TABLE invoices ADD COLUMN clerk_recommendation TEXT CHECK (clerk_recommendation IN ('APPROVE','REJECT'));
ALTER TABLE invoices ADD COLUMN clerk_note TEXT;

ALTER TABLE invoice_exceptions DROP CONSTRAINT IF EXISTS invoice_exceptions_exception_status_check;
ALTER TABLE invoice_exceptions ADD CONSTRAINT invoice_exceptions_exception_status_check
  CHECK (exception_status IN ('OPEN','ASSIGNED','CHECKED','RESOLVED','WAIVED'));
