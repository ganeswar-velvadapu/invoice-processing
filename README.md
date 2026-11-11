# Invoice Processing & PO Matching

Manual-upload AP system: ingest invoices/POs/GRNs → extract (PDF text, Tesseract OCR) → validate → match against POs and goods receipts (two/three-way, line level) → explainable deterministic decision → AP review with exceptions and a full audit trail. No LLM.

```
backend/    Go + Gin, one binary: `server api | worker | all | migrate`
frontend/   Next.js 16 + TypeScript + Tailwind (AP dashboard; proxies /api/* to the backend)
data/       sample master data (vendors, POs, GRNs as CSV) for the demo; see data/README.md
backend/testdata/  hand-made PDFs + procurement_import.json used by tests and scripts/e2e_smoke.py
scripts/    e2e_smoke.py — end-to-end scenario test against a running backend
```

## Run

1. `cp .env.example .env` and set `DATABASE_URL` (Neon, **direct** host, `sslmode=require`), `JWT_SECRET` (`openssl rand -hex 32`) and `ADMIN_PASSWORD`. Set `BUYER_GSTINS` to your own GSTIN(s).
2. **Docker (recommended, includes Tesseract + Poppler):** `docker compose up --build` → UI on http://localhost:3000, API on :8080. Originals are stored in the `storage` volume.
3. **Without Docker:** needs `pdftotext`/`pdftoppm` (poppler-utils) and `tesseract` on PATH.
   ```
   cd backend && go run ./cmd/server all          # API + worker, runs migrations
   cd frontend && npm install && npm run dev       # BACKEND_URL defaults to http://localhost:8080
   ```
4. Sign in with `ADMIN_EMAIL`/`ADMIN_PASSWORD`, add users under Settings.

Try it: follow `data/README.md` (import the CSVs, then upload invoices). `python3 scripts/e2e_smoke.py http://localhost:8080` runs an automated scenario with the fixtures in `backend/testdata/` against an empty database.

Extraction benchmark against a generator's ground truth (offline, never used by the app):
`python3 -I scripts/benchmark_extraction.py /path/to/generator/output --master` (`--master` passes the known GSTINs as the vendor master would).

Regenerate test fixtures: `cd backend && go run ./cmd/samplegen`. Debug extraction on any file: `go run ./cmd/extractcli file.pdf --text`.

## How it works

**Ingestion** (`internal/service/ingest.go`): the single entry point for every channel. Sniffs magic bytes (PDF/PNG/JPEG/TIFF), enforces the size limit, streams to `STORAGE_DIR/YYYY/MM/<uuid>.<ext>` while hashing (SHA-256), creates the `documents` row and queues a job. Email/ERP connectors later call `Ingest` with `SourceType`/`SourceReference`. Nothing else changes.

**Worker**: a PostgreSQL job queue (`processing_jobs`, `FOR UPDATE SKIP LOCKED`) with retries and backoff, plus recovery of stuck jobs. No Redis.

**Extraction** (`internal/extract`, `internal/parse`):
- `pdftotext -layout` per page. Pages without usable text are rendered (`pdftoppm`, 300 dpi) and OCR'd by Tesseract (TSV). Words are rebuilt into layout-preserving lines, so one parser handles both paths. OCR sits behind the `OCREngine` interface.
- Classification by weighted keywords (title zone weighs more). A user's declared type is a hint that strong evidence overrides.
- Header fields are found through label anchors (same line or the stacked "label above value" layout), value patterns and context. Examples: seller vs buyer GSTIN from nearby labels and your own `BUYER_GSTINS`, and GSTIN OCR repair validated by checksum.
- Line items: header-keyword column detection, with multi-line headers, wrapped descriptions and repeated headers on later pages. An arithmetic fallback (qty × price ≈ amount) covers tables without a recognisable header.
- Totals are cross-checked. If taxable + tax + other + round-off equals the grand total, confidence goes up.
- Confidence per field and per line, with page and bbox (OCR) where known. A GSTIN that matches the vendor master raises its confidence. Optional per-vendor regex profiles (Vendors page, admin) override fields for awkward layouts.
- Missing or low-confidence critical fields set the document to `EXTRACTION_REVIEW`. Corrections in the UI are saved as a new `MANUAL` extraction version, and all downstream steps re-run.

**Validation** (`internal/validate`): required fields, date sanity, GSTIN format + checksum (not registration status), buyer GSTIN, vendor approved/active/blocked plus identity consistency, currency, line arithmetic, GST structure (intra-state CGST=SGST vs inter-state IGST), totals, duplicates (identical file, or the same supplier GSTIN + normalised number + financial year; same date+amount under a different number gives a warning), and extraction confidence.

**Matching** (`internal/match`): manual AP choice > exact/normalised PO reference (vendor must match, PO must be open) > candidate search over the vendor's open POs (item code, HSN, description similarity, price, remaining quantity). A single strong candidate covering every line is auto-confirmed. Weaker or tied candidates go to REVIEW. With no candidate the result is `PO_NOT_FOUND`, and a PO is never created automatically. Invoice lines are allocated to PO lines, across several POs when needed. Per PO line it checks price (tolerance), GST rate (separate from price), cumulative invoiced vs ordered, and cumulative invoiced vs **accepted** GRN quantity. "Cumulative" counts earlier live invoices plus approved ones. Duplicates and rejected invoices are excluded.

**Decision** (`internal/decision`), derived only from persisted checks, in this precedence: blocked vendor → REJECT; duplicate → HOLD; missing mandatory data → HOLD; PO/GRN not yet available → PENDING_DOCUMENTS; ambiguous PO → REVIEW; quantity/price/tax/arithmetic/vendor mismatch → HOLD; low confidence/warnings → REVIEW; otherwise APPROVE. Each run stores every check (rule version + tolerance version), the decision JSON (summary, recommended action) and the matches. It syncs exceptions: new ones open, fixed ones auto-resolve, waived ones stay waived and no longer block.

**Status mapping**: APPROVE → `READY_FOR_VERIFICATION` (system checks passed, awaiting AP approval); REVIEW → `REVIEW_REQUIRED`; HOLD → `ON_HOLD`; PENDING → `PENDING_PO_MATCH` / `PENDING_GRN`. Human decisions (`APPROVED`, `REJECTED`, …) are never overwritten by re-verification. Document processing status is tracked separately.

**Any order**: storing a PO or GRN links waiting GRNs and re-queues open invoices. Approving or rejecting an invoice re-queues other open invoices on the same POs, because the available quantity changed.

**Roles** (JWT, enforced on the backend): `AP_CLERK` uploads, corrects, confirms PO matches, resolves exceptions and re-verifies. `AP_MANAGER` adds approve/reject/override (reason required) and waive, plus vendor/PO/GRN maintenance and imports. `ADMIN` adds users, tolerance ver  sions and vendor extraction profiles.

**Tolerances** are versioned rows (`tolerance_rules`). The initial version is price ±1% or ₹1, quantity 0, tax rounding ₹1, min. extraction confidence 0.75. Publishing a new version applies to later verifications.

## API

See `backend/internal/api/api.go` for the full route table. The main endpoints are `POST /api/documents` (multipart `file`, optional `document_type`), `GET/PUT /api/documents/:id/extraction`, `POST /api/imports/procurement` (JSON body, or multipart CSV with `kind`), `GET /api/invoices/:id` / `checks` / `exceptions` / `comparison` / `events`, `POST /api/invoices/:id/verify|approve|reject|override|po-match`, and `POST /api/exceptions/:id` (`RESOLVE|WAIVE|REOPEN|ASSIGN`).

## Known limits / next steps

- Phase 2: a reproducible synthetic dataset generator (clean/degraded/heavy/discrepant, partial deliveries, split invoices, multi-PO) plus an evaluation runner against the JSON ground truth. `cmd/samplegen` is the seed for it.
- MRN (material return) and e-way bill documents are classified and stored but not yet used in matching. Credit notes are not modelled yet.
- Money is handled as float64 rounded to paise, with NUMERIC(18,2) in the database.
- Email ingestion (later phase) should call `service.Ingest` with `SourceType: "EMAIL"`.
