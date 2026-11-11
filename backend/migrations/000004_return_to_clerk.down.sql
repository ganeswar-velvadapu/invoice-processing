UPDATE invoices SET invoice_status = 'REVIEW_REQUIRED' WHERE invoice_status = 'RETURNED';
ALTER TABLE invoices DROP COLUMN IF EXISTS assigned_to_user_id, DROP COLUMN IF EXISTS returned_at,
  DROP COLUMN IF EXISTS returned_by_user_id, DROP COLUMN IF EXISTS return_note;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_invoice_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_invoice_status_check CHECK (invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH',
  'PENDING_GRN','READY_FOR_VERIFICATION','REVIEW_REQUIRED','ON_HOLD','APPROVED','REJECTED','PAYMENT_PENDING','PAID'));
