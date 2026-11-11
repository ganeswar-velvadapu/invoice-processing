-- A manager can return an invoice to the clerk with a note; it stays RETURNED until the clerk resubmits.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_invoice_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_invoice_status_check CHECK (invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH',
  'PENDING_GRN','READY_FOR_VERIFICATION','REVIEW_REQUIRED','ON_HOLD','RETURNED','APPROVED','REJECTED','PAYMENT_PENDING','PAID'));
ALTER TABLE invoices ADD COLUMN return_note TEXT;
ALTER TABLE invoices ADD COLUMN returned_by_user_id UUID REFERENCES users(user_id);
ALTER TABLE invoices ADD COLUMN returned_at TIMESTAMPTZ;
ALTER TABLE invoices ADD COLUMN assigned_to_user_id UUID REFERENCES users(user_id);
