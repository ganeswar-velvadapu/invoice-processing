package service

import (
	"context"

	"invoiceproc/internal/classify"
	"invoiceproc/internal/db"
	"invoiceproc/internal/util"
)

// findPOByRef resolves a PO reference read from a document. Exact (normalised) matches win.
// Otherwise an OCR slip is tolerated only within the same vendor: at most one differing character
// (two for 12+ character numbers) and exactly one candidate. approx reports a tolerant match.
func findPOByRef(ctx context.Context, q db.Querier, ref, vendorID, vendorGSTIN string) (id, number string, approx bool) {
	norm := util.NormalizeRef(ref)
	if norm == "" {
		return "", "", false
	}
	if q.QueryRowContext(ctx, `SELECT purchase_order_id, po_number FROM purchase_orders WHERE po_number_normalized=$1`, norm).Scan(&id, &number) == nil {
		return id, number, false
	}
	if vendorID == "" && vendorGSTIN == "" {
		return "", "", false
	}
	rows, err := q.QueryContext(ctx, `SELECT purchase_order_id, po_number, po_number_normalized FROM purchase_orders
		WHERE (vendor_id = NULLIF($1,'')::uuid OR vendor_gstin = NULLIF($2,'')) AND length(po_number_normalized) BETWEEN $3 AND $4`,
		vendorID, vendorGSTIN, len(norm)-2, len(norm)+2)
	if err != nil {
		return "", "", false
	}
	defer rows.Close()
	limit := 1
	if len(norm) >= 12 {
		limit = 2
	}
	found := 0
	for rows.Next() {
		var pid, num, pn string
		if rows.Scan(&pid, &num, &pn) != nil {
			continue
		}
		if classify.Levenshtein(norm, pn) <= limit {
			found++
			id, number = pid, num
		}
	}
	if found == 1 {
		return id, number, true
	}
	return "", "", false
}
