# Edge cases

The edge cases are part of the normal demo set in `data/demo/`. They are invoices against the same POs and GRNs the admin imports for everything else, so there is no separate setup. See `data/demo/README.md` for the full table.

| Edge case | Files | What the system does |
|---|---|---|
| 1. Split invoices against one PO | 12, 13, 14 | Tracks billed quantity per PO line across all invoices. The two partial invoices pass; the third is held with "already invoiced". |
| 2. No PO number on the invoice | 15, 16 | Searches the supplier's open POs by items, quantities and prices. One clear match → used, with the reasoning shown. Several equally good → REVIEW; the clerk confirms one. |
| 3. Duplicate resubmission | 17, re-upload 01 | Same file → DUPLICATE_INVOICE. Same supplier + number + year → DUPLICATE_INVOICE. Same supplier, date and amount under a new number → POSSIBLE_DUPLICATE, usually with an over-billed PO as well. Always HOLD or REVIEW, never dropped. |
| 4. Knows when it doesn't know | 09, 10 (handwritten scans) | A digital PDF is only trusted if its totals reconcile. Scans go to the vision model, and fields it cannot back with evidence get low confidence → REVIEW. The clerk corrects them and the invoice is re-checked. |

## Clerk → manager on a held invoice (e.g. 04 or 14)

1. The clerk opens it. It opens on **Invoice vs PO vs GRN**, which shows the related documents and the line-by-line differences.
2. For each exception: **✓ Mark as checked** with a note saying what was verified.
3. **Send for approval** with a recommendation (approve or reject). Recommending approval requires every exception to be checked or resolved.
4. The manager opens **Awaiting approval**, reads the recommendation and notes, and decides: **Approve** (with a reason if the system said HOLD), **Reject** or **Return to clerk**.
5. Everything is recorded in the **Audit** tab, which admins can also see.
