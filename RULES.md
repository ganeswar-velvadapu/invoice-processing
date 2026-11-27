# Rules: every check the app runs

All decisions come from fixed rules (no AI). AI only **reads** documents; these rules **decide**. This file lists every rule, what it checks, what happens when it fails, and where it lives in the code.

## How to read this

Every check produces a result:

| Status | Meaning |
|---|---|
| **PASS** | the rule is satisfied |
| **WARNING** | something to look at; usually leads to REVIEW |
| **FAIL** | the rule is broken |

A WARNING or FAIL carries a **class**. The class decides the outcome and the exception's severity:

| Class | Decision | Severity | Examples |
|---|---|---|---|
| `REJECTING` | **REJECT** | CRITICAL | blocked vendor |
| `DUPLICATE` | **HOLD** | HIGH | same file or same invoice number again |
| `MISSING` | **HOLD** | HIGH | no invoice number / date / GSTIN / total / lines |
| `PENDING_PO` | **PENDING** (waiting for documents) | MEDIUM | no PO on file yet |
| `PENDING_GRN` | **PENDING** (waiting for documents) | MEDIUM | goods not received yet |
| `AMBIGUOUS` | **REVIEW** | MEDIUM | several POs fit equally well |
| `BLOCKING` | **HOLD** | HIGH | price, quantity, tax or arithmetic mismatch; vendor problems |
| `LOW_CONFIDENCE` | **REVIEW** | LOW | the reading isn't trustworthy enough |
| `WARN` | **REVIEW** | LOW | possible duplicate, odd dates, buyer GSTIN issues |
| *(no failures)* | **APPROVE** | — | everything passed |

**The decision is the first class in that table (top wins) among the failing checks.** A blocked vendor beats everything; a single warning on an otherwise clean invoice gives REVIEW. Waived exceptions are ignored.

Code: classes in [`model/model.go`](backend/internal/model/model.go), order in [`decision/decision.go`](backend/internal/decision/decision.go), severities in [`service/verify.go`](backend/internal/service/verify.go).

---

## 1. Checks on the invoice by itself: `validate/`

File: [`backend/internal/validate/validate.go`](backend/internal/validate/validate.go)

| Check | Rule | When it fails | Exception | Code |
|---|---|---|---|---|
| `REQUIRED_FIELDS` | invoice number, invoice date, supplier GSTIN, grand total and at least one line item are present | FAIL → `MISSING` → **HOLD** | `MISSING_REQUIRED_FIELDS` | [line 36](backend/internal/validate/validate.go:36) |
| `INVOICE_DATE_VALID` | the invoice date isn't in the future (beyond the date tolerance) | FAIL → `BLOCKING` → **HOLD** | — | [line 61](backend/internal/validate/validate.go:61) |
| | the invoice isn't more than a year old | WARNING → **REVIEW** | — | |
| | the due date isn't before the invoice date | WARNING → **REVIEW** | — | |
| `GSTIN_FORMAT` | the supplier GSTIN is valid (pattern, state code, checksum per `GSTIN_CHECKSUM`) | FAIL → **HOLD** | `INVALID_SELLER_GSTIN` | [line 79](backend/internal/validate/validate.go:79) |
| | only the checksum is wrong, with `GSTIN_CHECKSUM=warn` | WARNING → **REVIEW** | `GSTIN_CHECKSUM_MISMATCH` | |
| `BUYER_GSTIN` | the buyer GSTIN is valid | WARNING → **REVIEW** | `INVALID_BUYER_GSTIN` | [line 97](backend/internal/validate/validate.go:97) |
| | the buyer GSTIN is one of ours (`BUYER_GSTINS`; skipped if that setting is empty) | WARNING → **REVIEW** | `BUYER_GSTIN_NOT_OURS` | |
| `VENDOR_APPROVED` | a vendor with the supplier GSTIN exists | FAIL → **HOLD** | `VENDOR_NOT_FOUND` | [line 120](backend/internal/validate/validate.go:120) |
| | if the name matches a vendor but the GSTIN doesn't | FAIL → **HOLD** | `VENDOR_IDENTITY_MISMATCH` | |
| | the vendor isn't blocked | FAIL → `REJECTING` → **REJECT** | `VENDOR_BLOCKED` | |
| | the vendor is approved and active | FAIL → **HOLD** | `VENDOR_NOT_APPROVED` | |
| | the printed supplier name resembles the vendor's name (text similarity ≥ 0.3) | WARNING → **REVIEW** | `VENDOR_NAME_MISMATCH` | |
| `CURRENCY_SUPPORTED` | the currency is supported (INR) | FAIL → **HOLD** | `UNSUPPORTED_CURRENCY` | [line 146](backend/internal/validate/validate.go:146) |
| `LINE_VALUES_VALID` | every line has a quantity > 0 and a unit price ≥ 0 | FAIL → **HOLD** | `INVALID_LINE_VALUES` | [line 164](backend/internal/validate/validate.go:164) |
| `LINE_ARITHMETIC` | each line: quantity × price − discount = taxable value (± tax rounding) | FAIL → **HOLD** | `LINE_ARITHMETIC_ERROR` | |
| `TAX_ARITHMETIC` | same state (GSTIN codes equal) → CGST + SGST, no IGST, and CGST = SGST | FAIL → **HOLD** | `TAX_CALCULATION_ERROR` | [line 203](backend/internal/validate/validate.go:203) |
| | different states → IGST, no CGST/SGST | | | |
| | CGST + SGST + IGST = total tax | | | |
| | sum of line taxes = total tax | | | |
| | each line: tax = taxable value × GST rate | | | |
| `INVOICE_ARITHMETIC` | taxable + tax + other charges + round-off = grand total | FAIL → **HOLD** | `TOTAL_MISMATCH` | [line 241](backend/internal/validate/validate.go:241) |
| | sum of line taxable values = taxable total | | | |
| | round-off is at most ₹1 | | | |
| `DUPLICATE_INVOICE` | not an identical file (same SHA-256) already processed | FAIL → `DUPLICATE` → **HOLD** | `DUPLICATE_INVOICE` | [line 275](backend/internal/validate/validate.go:275) |
| | not the same supplier GSTIN + invoice number + financial year | FAIL → **HOLD** | `DUPLICATE_INVOICE` | |
| | not the same supplier + date + amount under a different number | WARNING → **REVIEW** | `POSSIBLE_DUPLICATE` | |
| | *(an invoice marked as an authorised revision passes)* | | | |
| `EXTRACTION_CONFIDENCE` | the reading's confidence is at least the minimum (default 0.75) | WARNING → `LOW_CONFIDENCE` → **REVIEW** | `LOW_EXTRACTION_CONFIDENCE` | [line 297](backend/internal/validate/validate.go:297) |

---

## 2. Checks against the PO and GRN: `match/`

File: [`backend/internal/match/match.go`](backend/internal/match/match.go)

### Finding the PO (`PO_MATCH`)
Tried in this order:

| Situation | Rule | Result | Exception |
|---|---|---|---|
| A clerk confirmed a PO | use it | PASS | — |
| The invoice prints a PO number (small typos tolerated, same vendor only) | the PO belongs to the invoice's vendor | FAIL → **HOLD** | `PO_VENDOR_MISMATCH` |
| | the PO is open (not cancelled, closed or draft) | FAIL → **HOLD** | `PO_NOT_OPEN` |
| | otherwise | PASS | — |
| No usable PO number: search the vendor's open POs by item code, HSN, description, price and remaining quantity | no PO fits | FAIL → `PENDING_PO` → **PENDING** | `PO_NOT_FOUND` |
| | two or more POs score within 0.15 per line of each other | WARNING → `AMBIGUOUS` → **REVIEW** | `MULTIPLE_PO_CANDIDATES` |
| | one PO matches every line with confidence ≥ 0.9 | PASS (confirmed automatically, with reasoning) | — |
| | a best PO exists but the evidence is weaker | WARNING → **REVIEW** | `PO_CANDIDATE_UNCONFIRMED` |

In the search, an invoice line counts as matching a PO line at a score ≥ 0.6; the score drops by 20% if the PO line hasn't enough quantity left, and by 30% if the PO is dated after the invoice. Code: [line 101](backend/internal/match/match.go:101) (reference / manual), [line 386](backend/internal/match/match.go:386) (search).

### Checks per line

| Check | Rule | When it fails | Exception | Code |
|---|---|---|---|---|
| `PO_LINE_MATCH` | every invoice line is paired with a line on the PO | FAIL → **HOLD** | `ITEM_NOT_ON_PO` | [line 239](backend/internal/match/match.go:239) |
| `PRICE_MATCH` | invoice unit price = PO price, within **±max(price %, price ₹)** (default ±1% or ₹1) | FAIL → **HOLD** | `PRICE_MISMATCH` | [line 264](backend/internal/match/match.go:264) |
| `TAX_RATE_MATCH` | GST rate on the invoice = GST rate on the PO (no tolerance) | FAIL → **HOLD** | `TAX_RATE_MISMATCH` | [line 280](backend/internal/match/match.go:280) |
| `QUANTITY_MATCH` | **cumulative** quantity billed (earlier invoices + this one) ≤ ordered × (1 + quantity %) | FAIL → **HOLD** | `QUANTITY_EXCEEDS_PO` | [line 302](backend/internal/match/match.go:302) |
| `GRN_ACCEPTED_QUANTITY` | a goods receipt exists for this PO line | FAIL → `PENDING_GRN` → **PENDING** | `GRN_NOT_RECEIVED` | [line 315](backend/internal/match/match.go:315) |
| | **cumulative** quantity billed ≤ quantity **accepted** on the GRNs × (1 + quantity %) | FAIL → **HOLD** | `QUANTITY_EXCEEDS_GRN` | |

"Cumulative" counts earlier invoices on the same PO line that are still live or approved; rejected invoices and duplicates are excluded, so rejecting one invoice can free quantity for another.

---

## 3. The decision: `decision/`

File: [`backend/internal/decision/decision.go`](backend/internal/decision/decision.go)

1. Collect the failing and warning checks (ignoring waived exceptions).
2. Walk the class order: **REJECTING → DUPLICATE → MISSING → PENDING_PO → PENDING_GRN → AMBIGUOUS → BLOCKING → LOW_CONFIDENCE → WARN**. The first class with a check decides: REJECT, HOLD, PENDING or REVIEW.
3. Nothing found → **APPROVE**.
4. The summary is the deciding check's reason (plus "+N more issues"), with a recommended action per exception.

| Decision | Invoice status |
|---|---|
| APPROVE | Ready for approval |
| REVIEW | Review required |
| HOLD | On hold |
| PENDING | Waiting for documents (PO / GRN) |
| REJECT | Rejected |

Human decisions (approved, rejected, returned, awaiting approval) are never overwritten by a re-check.

---

## 4. Tolerances (the numbers): database

The thresholds are **not in code**. They're in the `tolerance_rules` table, versioned, and editable by an admin under **Settings → Tolerance rules**. Each check run records which version it used.

| Setting | Default | Used by |
|---|---|---|
| Price tolerance % | 1% | `PRICE_MATCH` |
| Price tolerance ₹ | ₹1.00 | `PRICE_MATCH` (the larger of % and ₹ applies) |
| Quantity tolerance % | 0% | `QUANTITY_MATCH`, `GRN_ACCEPTED_QUANTITY` |
| Tax rounding ₹ | ₹1.00 | `LINE_ARITHMETIC`, `TAX_ARITHMETIC`, `INVOICE_ARITHMETIC` |
| Date tolerance (days) | 0 | `INVOICE_DATE_VALID` (future dates) |
| Minimum extraction confidence | 0.75 | `EXTRACTION_CONFIDENCE`, and which fields need review after reading |

Defaults are set by the first migration; the active version is loaded by [`ActiveTolerance`](backend/internal/service/service.go:86).

---

## 5. Can the reading be trusted? (reconciliation gate)

File: [`backend/internal/parse/reconcile.go`](backend/internal/parse/reconcile.go)

Before a **rules reading** of a digital PDF is used, the document must agree with itself. Any problem below sends the document to the **vision model** instead:

- at least one line item was read;
- every line: quantity × price = amount (for lines read with good confidence);
- the invoice number (and other key numbers) appear as complete words in the PDF's text;
- lines add up to the taxable value / subtotal;
- CGST + SGST + IGST = total tax;
- line taxes add up to total tax;
- a grand total was found, and taxable + tax + other + round-off = grand total;
- POs: the lines add up to the document total;
- GRNs: every line has a received and accepted quantity.

---

## 6. GSTIN rules

File: [`backend/internal/gstin/gstin.go`](backend/internal/gstin/gstin.go)

| Rule | Detail |
|---|---|
| Length | exactly 15 characters |
| Pattern | 2 digits (state) + 5 letters + 4 digits + 1 letter (the PAN) + entity character + `Z` + check character |
| State code | a valid Indian state code (first two digits) |
| Check character | computed from the first 14 characters; enforced per `GSTIN_CHECKSUM`: `strict` (invalid), `warn` (review), `off` (ignored) |
| Not checked | whether the GSTIN is actually registered and active with the government |

When reading documents, a GSTIN up to 3 characters away from a known vendor or buyer GSTIN is corrected to it, and seller and buyer are swapped if they were read the wrong way round.

---

## 7. Workflow rules (what people may do)

File: [`backend/internal/service/actions.go`](backend/internal/service/actions.go) and the routes in [`backend/internal/api/api.go`](backend/internal/api/api.go)

| Rule | Who / when |
|---|---|
| Approve directly | manager or admin; system decision APPROVE (ready for approval or awaiting approval) |
| Approve against the system | manager or admin; invoice awaiting approval; a written reason is required and recorded as `APPROVED_AGAINST_SYSTEM` |
| Reject, override, return to clerk | manager or admin; a reason or note is required |
| Send for approval, recommending **approve** | allowed only when no exception is still open (each must be checked or resolved first) |
| Send for approval, recommending **reject** | always allowed, also while waiting for documents |
| Mark an exception as checked | anyone logged in; a note is required; it still counts in the decision |
| Resolve an exception | anyone logged in; closes it as fixed |
| Waive an exception | manager or admin only; it no longer counts in the decision |
| Upload a PO or GRN | manager or admin only |
| PO or GRN arriving by email, or read from a clerk's upload | doesn't become master data until a manager confirms it |
| Users, tolerance rules, vendor extraction profiles | admin only |
| Import vendors, POs, GRNs | manager or admin |

---

## Adding a rule

1. Add the check in `validate.go` (invoice by itself) or `match.go` (against PO/GRN) with a status, a **class** and an exception code.
2. The decision picks it up automatically through its class; no change to `decision.go` is needed.
3. If it needs a threshold, add a column to `tolerance_rules` (a new migration) so admins can tune it.
4. Add a test in `validate`/`match` (`*_test.go`) with an invoice that should fail it.
