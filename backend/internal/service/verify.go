package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"invoiceproc/internal/db"
	"invoiceproc/internal/decision"
	"invoiceproc/internal/match"
	"invoiceproc/internal/model"
	"invoiceproc/internal/util"
	"invoiceproc/internal/validate"
)

var finalStatuses = map[string]bool{"APPROVED": true, "REJECTED": true, "PAYMENT_PENDING": true, "PAID": true}

var severityByClass = map[string]string{
	model.ClassReject: "CRITICAL", model.ClassDuplicate: "HIGH", model.ClassMissing: "HIGH", model.ClassBlocking: "HIGH",
	model.ClassPendingPO: "MEDIUM", model.ClassPendingGRN: "MEDIUM", model.ClassAmbiguous: "MEDIUM",
	model.ClassLowConf: "LOW", model.ClassWarn: "LOW",
}

type invRow struct {
	model.Invoice
	status, docDup string
	createdAt      time.Time
	isRevision     bool
}

func (s *Service) loadInvoice(ctx context.Context, q db.Querier, id string) (*invRow, error) {
	r := &invRow{}
	var num, vendor, sname, sg, bname, bg, cur, status, dup sql.NullString
	var idate, ddate sql.NullTime
	var sub, taxable, cg, sgst, ig, other, ro, tt, gt, conf sql.NullFloat64
	err := q.QueryRowContext(ctx, `SELECT i.invoice_id, i.document_id, i.invoice_number, i.invoice_date, i.due_date, i.vendor_id, i.seller_name,
		i.seller_gstin, i.buyer_name, i.buyer_gstin, i.po_references, i.currency, i.subtotal, i.taxable_amount, i.cgst_amount, i.sgst_amount,
		i.igst_amount, i.other_charges, i.round_off, i.total_tax, i.grand_total, i.extraction_confidence, i.invoice_status, i.created_at,
		i.is_revision, d.duplicate_of_document_id
		FROM invoices i JOIN documents d USING (document_id) WHERE i.invoice_id=$1`, id).Scan(
		&r.InvoiceID, &r.DocumentID, &num, &idate, &ddate, &vendor, &sname, &sg, &bname, &bg, textArray(&r.POReferences), &cur,
		&sub, &taxable, &cg, &sgst, &ig, &other, &ro, &tt, &gt, &conf, &status, &r.createdAt, &r.isRevision, &dup)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.InvoiceNumber, r.VendorID, r.SellerName, r.SellerGSTIN = num.String, vendor.String, sname.String, sg.String
	r.BuyerName, r.BuyerGSTIN, r.Currency, r.status, r.docDup = bname.String, bg.String, cur.String, status.String, dup.String
	if idate.Valid {
		r.InvoiceDate = &idate.Time
	}
	if ddate.Valid {
		r.DueDate = &ddate.Time
	}
	r.Subtotal, r.TaxableAmount, r.CGST, r.SGST, r.IGST = util.PtrF(sub), util.PtrF(taxable), util.PtrF(cg), util.PtrF(sgst), util.PtrF(ig)
	r.OtherCharges, r.RoundOff, r.TotalTax, r.GrandTotal = util.PtrF(other), util.PtrF(ro), util.PtrF(tt), util.PtrF(gt)
	r.ExtractionConfidence = conf.Float64

	rows, err := q.QueryContext(ctx, `SELECT invoice_item_id, line_number, item_code, description, hsn_sac, quantity, unit_of_measure,
		unit_price, discount, taxable_value, tax_rate, tax_amount, line_total FROM invoice_items WHERE invoice_id=$1 ORDER BY line_number`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it model.InvoiceItem
		var qn, up, tv, tr, ta, lt sql.NullFloat64
		if err := rows.Scan(&it.InvoiceItemID, &it.LineNumber, &it.ItemCode, &it.Description, &it.HSNSAC, &qn, &it.Unit, &up, &it.Discount,
			&tv, &tr, &ta, &lt); err != nil {
			return nil, err
		}
		it.Quantity, it.UnitPrice, it.TaxableValue, it.TaxRate, it.TaxAmount, it.LineTotal = util.PtrF(qn), util.PtrF(up), util.PtrF(tv), util.PtrF(tr), util.PtrF(ta), util.PtrF(lt)
		r.Items = append(r.Items, it)
	}
	return r, rows.Err()
}

// loadPOs loads POs with per-line accepted quantities and quantities already claimed by other live,
// earlier (or already approved) invoices: the cumulative context for partial deliveries/invoices.
func (s *Service) loadPOs(ctx context.Context, q db.Querier, where string, invoiceID string, invCreated time.Time, args ...any) ([]model.PurchaseOrder, error) {
	rows, err := q.QueryContext(ctx, `SELECT purchase_order_id, po_number, COALESCE(vendor_id::text,''), COALESCE(vendor_gstin,''), po_date, po_status
		FROM purchase_orders WHERE `+where+` ORDER BY po_date NULLS LAST, po_number`, args...)
	if err != nil {
		return nil, err
	}
	var pos []model.PurchaseOrder
	for rows.Next() {
		var p model.PurchaseOrder
		var d sql.NullTime
		if err := rows.Scan(&p.PurchaseOrderID, &p.PONumber, &p.VendorID, &p.VendorGSTIN, &d, &p.Status); err != nil {
			rows.Close()
			return nil, err
		}
		if d.Valid {
			p.PODate = &d.Time
		}
		pos = append(pos, p)
	}
	rows.Close()
	for i := range pos {
		p := &pos[i]
		ir, err := q.QueryContext(ctx, `SELECT poi.purchase_order_item_id, poi.line_number, poi.item_code, poi.description, poi.hsn_sac,
			poi.quantity_ordered, poi.unit_of_measure, poi.unit_price, poi.tax_rate,
			COALESCE((SELECT sum(gri.quantity_accepted) FROM goods_receipt_items gri JOIN goods_receipt_notes g USING (goods_receipt_note_id)
			          WHERE gri.purchase_order_item_id=poi.purchase_order_item_id AND g.receipt_status<>'CANCELLED'),0),
			EXISTS (SELECT 1 FROM goods_receipt_items gri JOIN goods_receipt_notes g USING (goods_receipt_note_id)
			          WHERE gri.purchase_order_item_id=poi.purchase_order_item_id AND g.receipt_status<>'CANCELLED'),
			COALESCE((SELECT sum(m.allocated_quantity) FROM invoice_po_item_matches m JOIN invoices i ON i.invoice_id=m.invoice_id
			          WHERE m.purchase_order_item_id=poi.purchase_order_item_id AND m.invoice_id<>$2
			          AND i.invoice_status<>'REJECTED' AND i.duplicate_of_invoice_id IS NULL
			          AND (i.created_at < $3 OR i.invoice_status IN ('APPROVED','PAYMENT_PENDING','PAID'))),0)
			FROM purchase_order_items poi WHERE poi.purchase_order_id=$1 ORDER BY poi.line_number`, p.PurchaseOrderID, invoiceID, invCreated)
		if err != nil {
			return nil, err
		}
		for ir.Next() {
			var it model.POItem
			it.PurchaseOrderID = p.PurchaseOrderID
			if err := ir.Scan(&it.POItemID, &it.LineNumber, &it.ItemCode, &it.Description, &it.HSNSAC, &it.QuantityOrdered, &it.Unit,
				&it.UnitPrice, &it.TaxRate, &it.QuantityAccepted, &it.HasReceipt, &it.QuantityInvoicedPrior); err != nil {
				ir.Close()
				return nil, err
			}
			p.Items = append(p.Items, it)
		}
		ir.Close()
		gr, err := q.QueryContext(ctx, `SELECT m.goods_receipt_note_id FROM grn_po_matches m JOIN goods_receipt_notes g USING (goods_receipt_note_id)
			WHERE m.purchase_order_id=$1 AND g.receipt_status<>'CANCELLED'`, p.PurchaseOrderID)
		if err != nil {
			return nil, err
		}
		for gr.Next() {
			var g string
			gr.Scan(&g)
			p.GRNIDs = append(p.GRNIDs, g)
		}
		gr.Close()
	}
	return pos, nil
}

func (s *Service) loadVendor(ctx context.Context, q db.Querier, where string, arg any) *model.Vendor {
	v := &model.Vendor{}
	var g sql.NullString
	err := q.QueryRowContext(ctx, `SELECT vendor_id, legal_name, gstin, is_approved, vendor_status FROM vendors WHERE `+where+` LIMIT 1`, arg).
		Scan(&v.VendorID, &v.LegalName, &g, &v.IsApproved, &v.Status)
	if err != nil {
		return nil
	}
	v.GSTIN = g.String
	return v
}

// Verify runs validation, matching and the decision engine and persists everything.
func (s *Service) Verify(ctx context.Context, invoiceID, userID string) (*decision.Result, error) {
	inv, err := s.loadInvoice(ctx, s.DB, invoiceID)
	if err != nil {
		return nil, err
	}
	tol, err := s.ActiveTolerance(ctx, s.DB)
	if err != nil {
		return nil, err
	}

	// vendor resolution (GSTIN first; a name-only match is an identity red flag, not a match)
	var vendor, byName *model.Vendor
	if inv.SellerGSTIN != "" {
		vendor = s.loadVendor(ctx, s.DB, "gstin=$1", inv.SellerGSTIN)
	}
	if vendor == nil && inv.SellerName != "" {
		rows, _ := s.DB.QueryContext(ctx, `SELECT legal_name FROM vendors`)
		if rows != nil {
			for rows.Next() {
				var n string
				rows.Scan(&n)
				if util.TextSimilarity(n, inv.SellerName) >= 0.6 {
					byName = s.loadVendor(ctx, s.DB, "legal_name=$1", n)
					break
				}
			}
			rows.Close()
		}
	}
	vendorID := ""
	if vendor != nil {
		vendorID = vendor.VendorID
	}
	if vendorID != inv.VendorID {
		s.DB.ExecContext(ctx, `UPDATE invoices SET vendor_id=$2 WHERE invoice_id=$1`, invoiceID, nullable(vendorID))
	}

	// duplicates
	dup := model.DuplicateInfo{IsRevision: inv.isRevision}
	if inv.docDup != "" {
		s.DB.QueryRowContext(ctx, `SELECT invoice_id, COALESCE(invoice_number,'') FROM invoices WHERE document_id=$1 AND invoice_id<>$2
			AND invoice_status<>'REJECTED'`, inv.docDup, invoiceID).Scan(&dup.SameFileInvoiceID, &dup.SameFileInvoiceNumber)
	}
	if inv.SellerGSTIN != "" && inv.InvoiceNumber != "" && inv.InvoiceDate != nil {
		s.DB.QueryRowContext(ctx, `SELECT invoice_id, invoice_number, invoice_status FROM invoices
			WHERE seller_gstin=$1 AND invoice_number_normalized=$2 AND financial_year=$3 AND invoice_id<>$4
			AND invoice_status<>'REJECTED' AND created_at < $5 ORDER BY created_at LIMIT 1`,
			inv.SellerGSTIN, util.NormalizeDocNumber(inv.InvoiceNumber), util.FinancialYear(*inv.InvoiceDate), invoiceID, inv.createdAt).
			Scan(&dup.BusinessKeyInvoiceID, &dup.BusinessKeyNumber, &dup.BusinessKeyStatus)
		if inv.GrandTotal != nil {
			rows, _ := s.DB.QueryContext(ctx, `SELECT invoice_id FROM invoices WHERE seller_gstin=$1 AND invoice_date=$2 AND grand_total=$3
				AND invoice_id<>$4 AND invoice_status<>'REJECTED' AND invoice_number_normalized IS DISTINCT FROM $5`,
				inv.SellerGSTIN, *inv.InvoiceDate, *inv.GrandTotal, invoiceID, util.NormalizeDocNumber(inv.InvoiceNumber))
			if rows != nil {
				for rows.Next() {
					var id string
					rows.Scan(&id)
					dup.SimilarInvoiceIDs = append(dup.SimilarInvoiceIDs, id)
				}
				rows.Close()
			}
		}
	}

	checks := validate.Run(validate.Input{Invoice: &inv.Invoice, Vendor: vendor, VendorByNameOnly: byName, Duplicates: dup, Tolerance: tol,
		BuyerGSTINs: s.Cfg.BuyerGSTINs, SupportedCurrencies: []string{"INR"}, Now: time.Now()})

	// matching inputs
	var refs []string
	for _, r := range inv.POReferences {
		if n := util.NormalizeRef(r); n != "" {
			refs = append(refs, n)
		}
	}
	mi := match.Input{Invoice: &inv.Invoice, VendorID: vendorID, Tolerance: tol, RejectedPOIDs: map[string]bool{}}
	mrows, err := s.DB.QueryContext(ctx, `SELECT purchase_order_id, match_status FROM invoice_po_matches WHERE invoice_id=$1 AND match_method='MANUAL'`, invoiceID)
	if err != nil {
		return nil, err
	}
	var manualIDs []string
	for mrows.Next() {
		var id, st string
		mrows.Scan(&id, &st)
		if st == "CONFIRMED" {
			manualIDs = append(manualIDs, id)
		} else if st == "REJECTED" {
			mi.RejectedPOIDs[id] = true
		}
	}
	mrows.Close()
	if len(manualIDs) > 0 {
		if mi.ManualPOs, err = s.loadPOs(ctx, s.DB, "purchase_order_id = ANY($1::uuid[])", invoiceID, inv.createdAt, manualIDs); err != nil {
			return nil, err
		}
	}
	if len(refs) > 0 {
		if mi.ReferencedPOs, err = s.loadPOs(ctx, s.DB, "po_number_normalized = ANY($1::text[])", invoiceID, inv.createdAt, refs); err != nil {
			return nil, err
		}
		found := map[string]bool{}
		for _, p := range mi.ReferencedPOs {
			found[util.NormalizeRef(p.PONumber)] = true
		}
		var approxIDs []string
		mi.ApproxRefs = map[string]string{}
		for i, r := range refs {
			if found[r] {
				continue
			}
			printed := inv.POReferences[min(i, len(inv.POReferences)-1)]
			// tolerate an OCR slip in the reference, within this vendor only
			if id, _, approx := findPOByRef(ctx, s.DB, r, vendorID, inv.SellerGSTIN); approx {
				approxIDs = append(approxIDs, id)
				mi.ApproxRefs[id] = printed
				continue
			}
			mi.MissingRefs = append(mi.MissingRefs, printed)
		}
		if len(approxIDs) > 0 {
			more, err := s.loadPOs(ctx, s.DB, "purchase_order_id = ANY($1::uuid[])", invoiceID, inv.createdAt, approxIDs)
			if err != nil {
				return nil, err
			}
			mi.ReferencedPOs = append(mi.ReferencedPOs, more...)
		}
	}
	if len(mi.ManualPOs) == 0 && len(mi.ReferencedPOs) == 0 && (vendorID != "" || inv.SellerGSTIN != "") {
		if mi.VendorPOs, err = s.loadPOs(ctx, s.DB, "(vendor_id = NULLIF($1,'')::uuid OR vendor_gstin = NULLIF($2,'')) AND po_status='APPROVED'",
			invoiceID, inv.createdAt, vendorID, inv.SellerGSTIN); err != nil {
			return nil, err
		}
	}
	mr := match.Run(mi)
	checks = append(checks, mr.Checks...)

	var waived = map[string]bool{}
	wrows, _ := s.DB.QueryContext(ctx, `SELECT exception_code FROM invoice_exceptions WHERE invoice_id=$1 AND exception_status='WAIVED'`, invoiceID)
	if wrows != nil {
		for wrows.Next() {
			var c string
			wrows.Scan(&c)
			waived[c] = true
		}
		wrows.Close()
	}
	var matchedPOs []string
	for _, m := range mr.POMatches {
		if m.Status == match.StatusConfirmed {
			matchedPOs = append(matchedPOs, m.PONumber)
		}
	}
	res := decision.Decide(inv.InvoiceNumber, matchedPOs, checks, waived, tol.Version)

	// persist
	runID := uuid.NewString()
	err = db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		// serialise concurrent verifications of the same invoice
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM invoices WHERE invoice_id=$1 FOR UPDATE`, invoiceID); err != nil {
			return err
		}
		for _, c := range checks {
			var ev any
			if c.Evidence != nil {
				ev = jsonb(c.Evidence)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_checks (invoice_id, verification_run_id, check_code, check_status, expected_value,
				actual_value, reason, evidence, rule_version, tolerance_rule_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10)`,
				invoiceID, runID, c.Code, c.Status, nullable(c.Expected), nullable(c.Actual), c.Reason, ev, model.RuleVersion, tol.Version); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_po_matches WHERE invoice_id=$1 AND match_method<>'MANUAL'`, invoiceID); err != nil {
			return err
		}
		for _, m := range mr.POMatches {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_po_matches (invoice_id, purchase_order_id, match_method, match_status,
				match_confidence, evidence) VALUES ($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT (invoice_id, purchase_order_id) DO NOTHING`,
				invoiceID, m.PurchaseOrderID, m.Method, m.Status, m.Confidence, jsonb(m.Evidence)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_po_item_matches WHERE invoice_id=$1`, invoiceID); err != nil {
			return err
		}
		for _, a := range mr.Allocations {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_po_item_matches (invoice_id, invoice_item_id, purchase_order_item_id,
				allocated_quantity, allocated_amount, match_basis) VALUES ($1,$2,$3,$4,$5,$6)`,
				invoiceID, a.InvoiceItemID, a.POItemID, a.Quantity, a.Amount, a.Basis); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_grn_matches WHERE invoice_id=$1`, invoiceID); err != nil {
			return err
		}
		for _, g := range mr.GRNIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_grn_matches (invoice_id, goods_receipt_note_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, invoiceID, g); err != nil {
				return err
			}
		}
		if err := s.syncExceptions(ctx, tx, invoiceID, checks, userID); err != nil {
			return err
		}
		status := res.InvoiceStatus
		if finalStatuses[inv.status] || inv.status == "RETURNED" || inv.status == "AWAITING_APPROVAL" { // people's workflow states are not overwritten by re-checks
			status = inv.status // human decisions are never overwritten by re-verification
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET decision=$2, decision_summary=$3, decision_detail=$4::jsonb, requires_human_review=$5,
			invoice_status=$6, last_verified_at=now(), updated_at=now() WHERE invoice_id=$1`,
			invoiceID, res.Decision, res.Summary, jsonb(res), res.RequiresHumanReview, status); err != nil {
			return err
		}
		docStatus := "UNMATCHED"
		if len(matchedPOs) > 0 {
			docStatus = "MATCHED"
		}
		tx.ExecContext(ctx, `UPDATE documents SET processing_status=$2, updated_at=now() WHERE document_id=$1 AND processing_status<>'EXTRACTION_REVIEW'`,
			inv.DocumentID, docStatus)
		desc := fmt.Sprintf("Verified: %s — %s", res.Decision, res.Summary)
		s.event(ctx, tx, invoiceID, inv.DocumentID, "VERIFIED", desc, userID, map[string]any{"verification_run_id": runID, "decision": res.Decision,
			"invoice_status": status, "deciding_checks": res.DecidingChecks, "matched_purchase_orders": matchedPOs, "rule_version": model.RuleVersion,
			"tolerance_version": tol.Version})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// syncExceptions opens exceptions for failing checks, refreshes open ones and auto-resolves
// those whose check now passes. Waived exceptions stay waived.
func (s *Service) syncExceptions(ctx context.Context, tx *sql.Tx, invoiceID string, checks []model.Check, userID string) error {
	type exc struct{ id, status string }
	existing := map[string]exc{}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT ON (exception_code) exception_code, invoice_exception_id, exception_status
		FROM invoice_exceptions WHERE invoice_id=$1 ORDER BY exception_code, created_at DESC`, invoiceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		var e exc
		rows.Scan(&code, &e.id, &e.status)
		existing[code] = e
	}
	rows.Close()
	failing := map[string][]string{}
	class := map[string]string{}
	var order []string
	for _, c := range checks {
		if c.Status == model.Pass || c.Status == model.NotRun || c.ExceptionCode == "" || c.Class == "" {
			continue
		}
		if _, ok := failing[c.ExceptionCode]; !ok {
			order = append(order, c.ExceptionCode)
		}
		failing[c.ExceptionCode] = append(failing[c.ExceptionCode], c.Reason)
		class[c.ExceptionCode] = c.Class
	}
	for _, code := range order {
		desc := strings.Join(failing[code], " ")
		e, ok := existing[code]
		switch {
		case ok && (e.status == "OPEN" || e.status == "ASSIGNED" || e.status == "CHECKED"): // CHECKED: looked at and explained, still failing
			if _, err := tx.ExecContext(ctx, `UPDATE invoice_exceptions SET description=$2, severity=$3 WHERE invoice_exception_id=$1`, e.id, desc, severityByClass[class[code]]); err != nil {
				return err
			}
		case ok && e.status == "WAIVED":
			// stays waived; the decision engine ignores it
		default:
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_exceptions (invoice_id, exception_code, severity, description) VALUES ($1,$2,$3,$4)`,
				invoiceID, code, severityByClass[class[code]], desc); err != nil {
				return err
			}
			s.event(ctx, tx, invoiceID, "", "EXCEPTION_OPENED", code+": "+desc, "", nil)
		}
	}
	for code, e := range existing {
		if _, still := failing[code]; still || (e.status != "OPEN" && e.status != "ASSIGNED" && e.status != "CHECKED") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoice_exceptions SET exception_status='RESOLVED', resolved_at=now(),
			resolution_note='Auto-resolved: the check passed on re-verification' WHERE invoice_exception_id=$1`, e.id); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, "", "EXCEPTION_AUTO_RESOLVED", code+" no longer applies after re-verification", "", nil)
	}
	return nil
}
