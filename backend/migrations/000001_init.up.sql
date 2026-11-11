-- Core schema for invoice processing and PO matching.
-- Primary keys use explicit names (<table>_id). Money is NUMERIC(18,2).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------- users
CREATE TABLE users (
    user_id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT NOT NULL UNIQUE,
    full_name      TEXT NOT NULL DEFAULT '',
    password_hash  TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('AP_CLERK','AP_MANAGER','ADMIN')),
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------- vendors
CREATE TABLE vendors (
    vendor_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name     TEXT NOT NULL,
    gstin          TEXT UNIQUE,
    address        TEXT NOT NULL DEFAULT '',
    is_approved    BOOLEAN NOT NULL DEFAULT FALSE,
    vendor_status  TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (vendor_status IN ('ACTIVE','INACTIVE','BLOCKED')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Optional vendor-specific extraction overrides: {"invoice_number": "regex with one group", ...}
CREATE TABLE vendor_extraction_profiles (
    vendor_extraction_profile_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id      UUID NOT NULL REFERENCES vendors(vendor_id) ON DELETE CASCADE,
    document_type  TEXT NOT NULL DEFAULT 'INVOICE',
    field_patterns JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, document_type)
);

-- ---------------------------------------------------------------- documents
CREATE TABLE documents (
    document_id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_type            TEXT NOT NULL DEFAULT 'OTHER'
                             CHECK (document_type IN ('PO','GRN','INVOICE','EWAY_BILL','MRN','OTHER')),
    declared_document_type   TEXT,
    classification_confidence NUMERIC(5,4),
    original_filename        TEXT NOT NULL,
    storage_path             TEXT NOT NULL,
    mime_type                TEXT NOT NULL,
    file_size_bytes          BIGINT NOT NULL,
    sha256_hash              TEXT NOT NULL,
    duplicate_of_document_id UUID REFERENCES documents(document_id),
    source_type              TEXT NOT NULL DEFAULT 'MANUAL' CHECK (source_type IN ('MANUAL','EMAIL','ERP','API')),
    source_reference         TEXT,
    uploaded_by_user_id      UUID REFERENCES users(user_id),
    received_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    processing_status        TEXT NOT NULL DEFAULT 'RECEIVED'
                             CHECK (processing_status IN ('RECEIVED','CLASSIFICATION_PENDING','EXTRACTION_PENDING',
                               'EXTRACTION_REVIEW','VALIDATION_PENDING','UNMATCHED','MATCHED','PROCESSING',
                               'PROCESSED','FAILED')),
    processing_stage         TEXT,
    processing_error         TEXT,
    page_count               INT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX documents_sha256_idx ON documents (sha256_hash);
CREATE INDEX documents_status_idx ON documents (processing_status);

-- Extraction results (versioned: every correction adds a new version).
CREATE TABLE document_extractions (
    document_extraction_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id        UUID NOT NULL REFERENCES documents(document_id) ON DELETE CASCADE,
    version            INT NOT NULL,
    extraction_method  TEXT NOT NULL CHECK (extraction_method IN ('PDF_TEXT','OCR','MANUAL')),
    raw_text           TEXT NOT NULL DEFAULT '',
    extracted_data     JSONB NOT NULL,          -- fields with value/confidence/page/source
    overall_confidence NUMERIC(5,4),
    ocr_mean_confidence NUMERIC(6,2),
    low_confidence_fields TEXT[] NOT NULL DEFAULT '{}',
    created_by_user_id UUID REFERENCES users(user_id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, version)
);

-- ---------------------------------------------------------------- job queue
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

-- ---------------------------------------------------------------- purchase orders
CREATE TABLE purchase_orders (
    purchase_order_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID REFERENCES documents(document_id),
    po_number       TEXT NOT NULL,
    po_number_normalized TEXT NOT NULL UNIQUE,
    vendor_id       UUID REFERENCES vendors(vendor_id),
    vendor_gstin    TEXT,
    buyer_entity    TEXT NOT NULL DEFAULT '',
    buyer_gstin     TEXT,
    po_date         DATE,
    currency        TEXT NOT NULL DEFAULT 'INR',
    po_status       TEXT NOT NULL DEFAULT 'APPROVED' CHECK (po_status IN ('DRAFT','APPROVED','CANCELLED','CLOSED')),
    total_amount    NUMERIC(18,2),
    source          TEXT NOT NULL DEFAULT 'IMPORT' CHECK (source IN ('IMPORT','DOCUMENT','MANUAL')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX purchase_orders_vendor_idx ON purchase_orders (vendor_id);

CREATE TABLE purchase_order_items (
    purchase_order_item_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_order_id UUID NOT NULL REFERENCES purchase_orders(purchase_order_id) ON DELETE CASCADE,
    line_number     INT NOT NULL,
    item_code       TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    hsn_sac         TEXT NOT NULL DEFAULT '',
    quantity_ordered NUMERIC(18,3) NOT NULL,
    unit_of_measure TEXT NOT NULL DEFAULT '',
    unit_price      NUMERIC(18,4) NOT NULL,
    tax_rate        NUMERIC(6,3) NOT NULL DEFAULT 0,
    line_total      NUMERIC(18,2) NOT NULL DEFAULT 0,
    UNIQUE (purchase_order_id, line_number)
);

-- ---------------------------------------------------------------- goods receipts
CREATE TABLE goods_receipt_notes (
    goods_receipt_note_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID REFERENCES documents(document_id),
    grn_number      TEXT NOT NULL,
    grn_number_normalized TEXT NOT NULL UNIQUE,
    po_reference    TEXT,
    purchase_order_id UUID REFERENCES purchase_orders(purchase_order_id),
    vendor_id       UUID REFERENCES vendors(vendor_id),
    vendor_gstin    TEXT,
    receipt_date    DATE,
    receipt_status  TEXT NOT NULL DEFAULT 'RECEIVED' CHECK (receipt_status IN ('RECEIVED','PARTIAL','CANCELLED')),
    source          TEXT NOT NULL DEFAULT 'IMPORT' CHECK (source IN ('IMPORT','DOCUMENT','MANUAL')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE goods_receipt_items (
    goods_receipt_item_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goods_receipt_note_id UUID NOT NULL REFERENCES goods_receipt_notes(goods_receipt_note_id) ON DELETE CASCADE,
    purchase_order_item_id UUID REFERENCES purchase_order_items(purchase_order_item_id),
    line_number     INT NOT NULL,
    item_code       TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    quantity_received NUMERIC(18,3) NOT NULL DEFAULT 0,
    quantity_accepted NUMERIC(18,3) NOT NULL DEFAULT 0,
    quantity_rejected NUMERIC(18,3) NOT NULL DEFAULT 0,
    unit_of_measure TEXT NOT NULL DEFAULT ''
);

CREATE TABLE grn_po_matches (
    grn_po_match_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goods_receipt_note_id UUID NOT NULL REFERENCES goods_receipt_notes(goods_receipt_note_id) ON DELETE CASCADE,
    purchase_order_id UUID NOT NULL REFERENCES purchase_orders(purchase_order_id) ON DELETE CASCADE,
    match_method    TEXT NOT NULL CHECK (match_method IN ('EXACT_REFERENCE','RULE_BASED','MANUAL')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (goods_receipt_note_id, purchase_order_id)
);

-- ---------------------------------------------------------------- invoices
CREATE TABLE invoices (
    invoice_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL UNIQUE REFERENCES documents(document_id),
    invoice_number  TEXT,
    invoice_number_normalized TEXT,
    invoice_date    DATE,
    due_date        DATE,
    financial_year  TEXT,
    vendor_id       UUID REFERENCES vendors(vendor_id),
    seller_name     TEXT,
    seller_gstin    TEXT,
    seller_address  TEXT,
    buyer_name      TEXT,
    buyer_gstin     TEXT,
    po_references   TEXT[] NOT NULL DEFAULT '{}',
    currency        TEXT NOT NULL DEFAULT 'INR',
    payment_terms   TEXT,
    subtotal        NUMERIC(18,2),
    taxable_amount  NUMERIC(18,2),
    cgst_amount     NUMERIC(18,2),
    sgst_amount     NUMERIC(18,2),
    igst_amount     NUMERIC(18,2),
    other_charges   NUMERIC(18,2),
    round_off       NUMERIC(18,2),
    total_tax       NUMERIC(18,2),
    grand_total     NUMERIC(18,2),
    extraction_confidence NUMERIC(5,4),
    invoice_status  TEXT NOT NULL DEFAULT 'PENDING_DOCUMENTS'
                    CHECK (invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH','PENDING_GRN',
                      'READY_FOR_VERIFICATION','REVIEW_REQUIRED','ON_HOLD','APPROVED','REJECTED',
                      'PAYMENT_PENDING','PAID')),
    decision        TEXT CHECK (decision IN ('APPROVE','REVIEW','HOLD','REJECT','PENDING_DOCUMENTS')),
    decision_summary TEXT,
    decision_detail JSONB,
    requires_human_review BOOLEAN NOT NULL DEFAULT TRUE,
    duplicate_of_invoice_id UUID REFERENCES invoices(invoice_id),
    is_revision     BOOLEAN NOT NULL DEFAULT FALSE,
    last_verified_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Business uniqueness: only one "live" original per (seller, number, FY). Suspected duplicates
-- are stored with duplicate_of_invoice_id set; revisions are flagged explicitly.
CREATE UNIQUE INDEX invoices_business_key_uq ON invoices (seller_gstin, invoice_number_normalized, financial_year)
    WHERE duplicate_of_invoice_id IS NULL AND is_revision = FALSE AND invoice_status <> 'REJECTED'
      AND seller_gstin IS NOT NULL AND invoice_number_normalized IS NOT NULL AND financial_year IS NOT NULL;
CREATE INDEX invoices_status_idx ON invoices (invoice_status);
CREATE INDEX invoices_vendor_idx ON invoices (vendor_id);

CREATE TABLE invoice_items (
    invoice_item_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    line_number     INT NOT NULL,
    item_code       TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    hsn_sac         TEXT NOT NULL DEFAULT '',
    quantity        NUMERIC(18,3),
    unit_of_measure TEXT NOT NULL DEFAULT '',
    unit_price      NUMERIC(18,4),
    discount        NUMERIC(18,2) NOT NULL DEFAULT 0,
    taxable_value   NUMERIC(18,2),
    tax_rate        NUMERIC(6,3),
    tax_amount      NUMERIC(18,2),
    line_total      NUMERIC(18,2),
    confidence      NUMERIC(5,4)
);

-- ---------------------------------------------------------------- matches
CREATE TABLE invoice_po_matches (
    invoice_po_match_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    purchase_order_id UUID NOT NULL REFERENCES purchase_orders(purchase_order_id) ON DELETE CASCADE,
    match_method    TEXT NOT NULL CHECK (match_method IN ('EXACT_REFERENCE','RULE_BASED','MANUAL')),
    match_status    TEXT NOT NULL CHECK (match_status IN ('CANDIDATE','CONFIRMED','REJECTED')),
    match_confidence NUMERIC(5,4),
    evidence        JSONB NOT NULL DEFAULT '[]'::jsonb,
    matched_by_user_id UUID REFERENCES users(user_id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (invoice_id, purchase_order_id)
);

CREATE TABLE invoice_po_item_matches (
    invoice_po_item_match_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    invoice_item_id UUID NOT NULL REFERENCES invoice_items(invoice_item_id) ON DELETE CASCADE,
    purchase_order_item_id UUID NOT NULL REFERENCES purchase_order_items(purchase_order_item_id) ON DELETE CASCADE,
    allocated_quantity NUMERIC(18,3) NOT NULL,
    allocated_amount NUMERIC(18,2) NOT NULL,
    match_basis     TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_po_item_matches_poi_idx ON invoice_po_item_matches (purchase_order_item_id);

CREATE TABLE invoice_grn_matches (
    invoice_grn_match_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    goods_receipt_note_id UUID NOT NULL REFERENCES goods_receipt_notes(goods_receipt_note_id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (invoice_id, goods_receipt_note_id)
);

-- ---------------------------------------------------------------- rules
CREATE TABLE tolerance_rules (
    tolerance_rule_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version         INT NOT NULL UNIQUE,
    price_tolerance_pct NUMERIC(8,4) NOT NULL,
    price_tolerance_abs NUMERIC(18,2) NOT NULL,
    quantity_tolerance_pct NUMERIC(8,4) NOT NULL,
    tax_rounding_abs NUMERIC(18,2) NOT NULL,
    date_tolerance_days INT NOT NULL DEFAULT 0,
    min_extraction_confidence NUMERIC(5,4) NOT NULL DEFAULT 0.75,
    is_active       BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id UUID REFERENCES users(user_id),
    note            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tolerance_rules_one_active ON tolerance_rules (is_active) WHERE is_active;
INSERT INTO tolerance_rules (version, price_tolerance_pct, price_tolerance_abs, quantity_tolerance_pct,
                             tax_rounding_abs, date_tolerance_days, min_extraction_confidence, is_active, note)
VALUES (1, 1.0, 1.00, 0, 1.00, 0, 0.75, TRUE, 'Initial policy: price ±1% or ₹1, qty 0, tax rounding ₹1');

-- ---------------------------------------------------------------- checks / exceptions / audit
CREATE TABLE invoice_checks (
    invoice_check_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    verification_run_id UUID NOT NULL,
    check_code      TEXT NOT NULL,
    check_status    TEXT NOT NULL CHECK (check_status IN ('PASS','FAIL','WARNING','NOT_RUN')),
    expected_value  TEXT,
    actual_value    TEXT,
    reason          TEXT NOT NULL,
    evidence        JSONB,
    rule_version    TEXT NOT NULL,
    tolerance_rule_version INT,
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_checks_invoice_idx ON invoice_checks (invoice_id, checked_at DESC);

CREATE TABLE invoice_exceptions (
    invoice_exception_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    exception_code  TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('LOW','MEDIUM','HIGH','CRITICAL')),
    description     TEXT NOT NULL,
    exception_status TEXT NOT NULL DEFAULT 'OPEN' CHECK (exception_status IN ('OPEN','ASSIGNED','RESOLVED','WAIVED')),
    assigned_to_user_id UUID REFERENCES users(user_id),
    resolution_note TEXT,
    resolved_by_user_id UUID REFERENCES users(user_id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX invoice_exceptions_open_idx ON invoice_exceptions (exception_status, invoice_id);

CREATE TABLE invoice_processing_events (
    invoice_processing_event_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID REFERENCES invoices(invoice_id) ON DELETE CASCADE,
    document_id     UUID REFERENCES documents(document_id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    event_description TEXT NOT NULL,
    actor_type      TEXT NOT NULL CHECK (actor_type IN ('SYSTEM','USER')),
    actor_id        UUID,
    event_data      JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_processing_events_inv_idx ON invoice_processing_events (invoice_id, created_at);
CREATE INDEX invoice_processing_events_doc_idx ON invoice_processing_events (document_id, created_at);
