# Demo dataset

Buyer: **Inno Manufacturing Ltd**, GSTIN `29AAACI1234F1ZY` (Bengaluru, Karnataka). There are six suppliers in five invoice layouts, and every invoice is planned against the master data so its outcome is known in advance. The edge cases (12–17) use the same ordinary POs and GRNs as everything else: there is nothing special to import for them.

Regenerate the whole set with:

```bash
~/Practice/invoice-generator/invoice-generator/bin/python scripts/build_demo_set.py
```

It needs Playwright, ImageMagick and img2pdf, plus internet access for the handwriting fonts.

## 1. Master data (admin or manager → Imports)

Import these in order, choosing the matching type for each:

1. `vendors.csv`: 6 approved suppliers.
2. `purchase_orders.csv`: 14 POs.
3. `goods_receipt_notes.csv`: 13 GRNs. PO/26-27/0433 has no GRN yet on purpose, and GRN/26-27/1062 rejected 6 of the 60 LED panels.

## 2. Invoices (clerk → Documents → upload)

| File | Layout | Expected | What it shows |
|---|---|---|---|
| 01_shakti_clean | classic bordered, IGST | **APPROVE** | Clean three-way match |
| 02_shakti_price_above_po | classic | **HOLD**: PRICE_MISMATCH | 74.25/kg billed against PO price 69.75 |
| 03_bharat_clean | Tally-style, CGST + SGST | **APPROVE** | Intra-state tax split, negative round-off |
| 04_bharat_short_receipt | Tally-style | **HOLD**: QUANTITY_EXCEEDS_GRN | Billed 60, but the GRN accepted 54 |
| 05_orion_eight_lines | modern colour banner | **APPROVE** | 8 lines with mixed 12% and 18% IGST shown per rate |
| 06_orion_wrong_tax_rate | modern | **HOLD**: TAX_RATE_MISMATCH | Boxes charged 12% where the PO says 18% |
| 07_nilgiri_hsn_summary | boxed grid with an HSN-wise tax table | **APPROVE** | The HSN summary must not be read as extra items |
| 08_nilgiri_goods_not_received | boxed grid | **PENDING_DOCUMENTS**: GRN_NOT_RECEIVED | Re-checks automatically once a GRN is imported |
| 09_ganga_handwritten | handwritten bill book, scanned | **APPROVE or REVIEW** | No text layer, so the vision model reads it |
| 10_kaveri_handwritten | second handwriting style, skewed scan | **APPROVE or REVIEW** | Vision model; anything it can't verify goes to REVIEW |
| 11_apex_unknown_vendor | classic | **PENDING_DOCUMENTS**: VENDOR_NOT_FOUND + PO_NOT_FOUND | Unknown supplier, no PO: never auto-approved |
| 12_bharat_split_part1 | Tally | **APPROVE** | *Edge 1*: first partial invoice on PO/26-27/0442 |
| 13_bharat_split_part2 | Tally | **APPROVE** | *Edge 1*: second partial invoice closes the PO exactly |
| 14_bharat_split_overbill | Tally | **HOLD**: QUANTITY_EXCEEDS_PO | *Edge 1*: fine on its own, but the PO is already fully invoiced |
| 15_nilgiri_no_po_number | grid | **APPROVE** | *Edge 2*: no PO printed; the only matching open PO (0445) is found and the reasoning shown |
| 16_shakti_no_po_ambiguous | classic | **REVIEW**: MULTIPLE_PO_CANDIDATES | *Edge 2*: two identical open POs; the system won't guess. Clerk confirms one → APPROVE |
| 17_bharat_renumbered_duplicate | Tally | **HOLD**: POSSIBLE_DUPLICATE | *Edge 3*: invoice 03 re-sent as 778A (same date and amount) |
| *01 uploaded again* | | **HOLD**: DUPLICATE_INVOICE | *Edge 3*: identical file already processed |

All files except 09 and 10 are digital PDFs. They take the fast path (text layer + rules + reconciliation gate) and are decided within seconds. Upload them in number order, because the split and duplicate cases depend on what came before. All 15, plus the re-upload and the PO pick on 16, were verified end to end. *Edge 4* (the system knows when it doesn't know) is 09 and 10. Files 09 and 10 are image-only scans and take a few minutes each on the local vision model. `expected.csv` lists the same expectations in machine-readable form.

## Good demo moments

- **08 → import a GRN for PO/26-27/0433** (add rows to a CSV and import it). The invoice moves from PENDING to APPROVE without anyone touching it.
- **04**: the clerk opens the PO/GRN comparison, marks the exception as checked ("6 panels returned, credit note requested") and sends it for approval with a recommendation to reject or approve. The manager decides.
- **Upload 01 again**: HOLD for DUPLICATE_INVOICE.
- **09 or 10**: open Extraction to see the model's reading next to the original. If anything was unsure, the clerk corrects it and the invoice is re-checked.

## Email demo (`email/`)

Three clean invoices to send to the AP mailbox:

| File | Layout | PO | Expected |
|---|---|---|---|
| 18_shakti_gi_pipe | classic | PO/26-27/0448 (already in the main import; resolve invoice 16 to **0447** so 0448 stays free) | **APPROVE** |
| 19_orion_packaging | modern | PO/26-27/0451 | **APPROVE** |
| 20_bharat_lighting | Tally, CGST + SGST | PO/26-27/0453 | **APPROVE** |

Before emailing 19 and 20, a manager imports the "new ERP export": `email/new_purchase_orders.csv` (as purchase orders), then `email/new_goods_receipt_notes.csv` (as goods receipt notes). All three are digital, so they're decided within seconds of being picked up.
