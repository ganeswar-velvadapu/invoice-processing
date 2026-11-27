package handlers

// Invoices: details, checks, PO matching and the approval workflow.

import (
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/utils"
)

type reasonBody struct {
	Reason       string `json:"reason"`
	Note         string `json:"note"`
	TargetStatus string `json:"target_status"`
}

func (h *Handler) ListInvoices(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT i.invoice_id, i.document_id, i.invoice_number, i.invoice_date, i.due_date, i.seller_name, i.seller_gstin, i.grand_total,
		i.currency, i.invoice_status, i.decision, i.decision_summary, i.requires_human_review, i.extraction_confidence, i.duplicate_of_invoice_id,
		i.po_references, i.last_verified_at, i.created_at, v.legal_name AS vendor_name,
		ARRAY(SELECT p.po_number FROM invoice_po_matches m JOIN purchase_orders p USING (purchase_order_id)
			WHERE m.invoice_id=i.invoice_id AND m.match_status='CONFIRMED') AS matched_pos,
		(SELECT count(*) FROM invoice_exceptions e WHERE e.invoice_id=i.invoice_id AND e.exception_status IN ('OPEN','ASSIGNED')) AS open_exceptions
		FROM invoices i LEFT JOIN vendors v USING (vendor_id)
		WHERE ($1='' OR i.invoice_status = ANY(string_to_array($1, ','))) AND ($2='' OR i.decision=$2)
		AND ($3='' OR i.invoice_number ILIKE '%'||$3||'%' OR i.seller_name ILIKE '%'||$3||'%' OR v.legal_name ILIKE '%'||$3||'%' OR i.seller_gstin ILIKE '%'||$3||'%')
		ORDER BY i.created_at DESC`, c.Query("status"), c.Query("decision"), c.Query("q"))
}

func (h *Handler) GetInvoice(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	inv, err := utils.Row(c, h.DB, `SELECT i.*, v.legal_name AS vendor_name, v.is_approved AS vendor_approved, v.vendor_status,
		d.original_filename, d.mime_type, d.processing_status AS document_status, d.duplicate_of_document_id,
		(SELECT invoice_number FROM invoices o WHERE o.invoice_id=i.duplicate_of_invoice_id) AS duplicate_of_invoice_number,
		(SELECT email FROM users WHERE user_id=i.returned_by_user_id) AS returned_by,
		(SELECT email FROM users WHERE user_id=i.submitted_by_user_id) AS submitted_by,
		(SELECT email FROM users WHERE user_id=i.assigned_to_user_id) AS assigned_to,
		(SELECT overall_confidence FROM document_extractions e WHERE e.document_id=i.document_id ORDER BY version DESC LIMIT 1) AS latest_extraction_confidence,
		(SELECT low_confidence_fields FROM document_extractions e WHERE e.document_id=i.document_id ORDER BY version DESC LIMIT 1) AS low_confidence_fields
		FROM invoices i JOIN documents d USING (document_id) LEFT JOIN vendors v ON v.vendor_id=i.vendor_id WHERE i.invoice_id=$1`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	items, err := utils.Rows(c, h.DB, `SELECT * FROM invoice_items WHERE invoice_id=$1 ORDER BY line_number`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	poMatches, err := utils.Rows(c, h.DB, `SELECT m.*, p.po_number, p.po_date, p.po_status, p.total_amount,
		p.document_id AS po_document_id, p.source AS po_source,
		(SELECT email FROM users WHERE user_id=m.matched_by_user_id) AS matched_by
		FROM invoice_po_matches m JOIN purchase_orders p USING (purchase_order_id) WHERE m.invoice_id=$1
		ORDER BY m.match_status, m.match_confidence DESC`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	receipts, err := utils.Rows(c, h.DB, `SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status, g.document_id, g.source
		FROM invoice_grn_matches m JOIN goods_receipt_notes g USING (goods_receipt_note_id) WHERE m.invoice_id=$1`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	inv["items"] = items
	inv["po_matches"] = poMatches
	inv["goods_receipts"] = receipts
	utils.OK(c, inv)
}

func (h *Handler) Verify(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	res, err := h.Svc.Verify(c, h.DB, id, auth.CurrentUserID(c))
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, res)
}

func (h *Handler) InvoiceChecks(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	if c.Query("all") == "true" {
		utils.SendRows(c, h.DB, `SELECT * FROM invoice_checks WHERE invoice_id=$1 ORDER BY checked_at DESC`, id)
		return
	}
	// only the latest verification run, failures first
	utils.SendRows(c, h.DB, `SELECT * FROM invoice_checks WHERE invoice_id=$1
		AND verification_run_id = (SELECT verification_run_id FROM invoice_checks WHERE invoice_id=$1 ORDER BY checked_at DESC LIMIT 1)
		ORDER BY CASE check_status WHEN 'FAIL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END, check_code`, id)
}

func (h *Handler) InvoiceExceptions(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	utils.SendRows(c, h.DB, `SELECT x.*, ua.email AS assigned_to, ur.email AS resolved_by
		FROM invoice_exceptions x LEFT JOIN users ua ON ua.user_id=x.assigned_to_user_id LEFT JOIN users ur ON ur.user_id=x.resolved_by_user_id
		WHERE x.invoice_id=$1 ORDER BY x.created_at DESC`, id)
}

func (h *Handler) InvoiceEvents(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	utils.SendRows(c, h.DB, `SELECT ev.*, u.email AS actor_email FROM invoice_processing_events ev
		LEFT JOIN users u ON u.user_id=ev.actor_id
		WHERE ev.invoice_id=$1 OR ev.document_id=(SELECT document_id FROM invoices WHERE invoice_id=$1)
		ORDER BY ev.created_at DESC`, id)
}

// comparison returns invoice lines side by side with their PO lines and goods receipts.
// Comparison returns invoice lines side by side with their PO lines and goods receipts.
// Three queries (lines, their PO lines, the receipts of those PO lines) put together here.
func (h *Handler) Comparison(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	if _, err := utils.Row(c, h.DB, `SELECT invoice_id FROM invoices WHERE invoice_id=$1`, id); err != nil {
		utils.Fail(c, err) // 404 for an unknown invoice
		return
	}
	lines, err := utils.Rows(c, h.DB, `SELECT invoice_item_id, line_number, item_code, description, hsn_sac, quantity, unit_of_measure, unit_price,
		tax_rate, taxable_value, confidence FROM invoice_items WHERE invoice_id=$1 ORDER BY line_number`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	poLines, err := utils.Rows(c, h.DB, `SELECT m.invoice_item_id, poi.purchase_order_item_id,
		p.po_number, p.purchase_order_id, poi.line_number AS po_line, poi.item_code, poi.description, poi.quantity_ordered, poi.unit_price, poi.tax_rate,
		m.allocated_quantity, m.match_basis,
		COALESCE((SELECT sum(g.quantity_received) FROM goods_receipt_items g WHERE g.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_received,
		COALESCE((SELECT sum(g.quantity_accepted) FROM goods_receipt_items g WHERE g.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_accepted,
		COALESCE((SELECT sum(o.allocated_quantity) FROM invoice_po_item_matches o JOIN invoices iv ON iv.invoice_id=o.invoice_id
			WHERE o.purchase_order_item_id=poi.purchase_order_item_id AND o.invoice_id<>$1 AND iv.invoice_status<>'REJECTED'
			AND iv.duplicate_of_invoice_id IS NULL),0) AS quantity_invoiced_other
		FROM invoice_po_item_matches m JOIN purchase_order_items poi USING (purchase_order_item_id)
		JOIN purchase_orders p ON p.purchase_order_id=poi.purchase_order_id
		WHERE m.invoice_item_id IN (SELECT invoice_item_id FROM invoice_items WHERE invoice_id=$1)`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	receipts, err := utils.Rows(c, h.DB, `SELECT g.purchase_order_item_id, gn.grn_number, gn.receipt_date,
		g.quantity_received AS received, g.quantity_accepted AS accepted, g.quantity_rejected AS rejected
		FROM goods_receipt_items g JOIN goods_receipt_notes gn USING (goods_receipt_note_id)
		WHERE g.purchase_order_item_id IN (SELECT m.purchase_order_item_id FROM invoice_po_item_matches m
			JOIN invoice_items it USING (invoice_item_id) WHERE it.invoice_id=$1)`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}

	// receipts per PO line
	receiptsByPOItem := map[any][]map[string]any{}
	for _, r := range receipts {
		key := r["purchase_order_item_id"]
		delete(r, "purchase_order_item_id")
		receiptsByPOItem[key] = append(receiptsByPOItem[key], r)
	}
	// PO lines (with their receipts) per invoice line
	poLinesByItem := map[any][]map[string]any{}
	for _, pl := range poLines {
		pl["receipts"] = receiptsByPOItem[pl["purchase_order_item_id"]]
		if pl["receipts"] == nil {
			pl["receipts"] = []map[string]any{}
		}
		key := pl["invoice_item_id"]
		delete(pl, "invoice_item_id")
		delete(pl, "purchase_order_item_id")
		poLinesByItem[key] = append(poLinesByItem[key], pl)
	}
	for _, line := range lines {
		line["po_lines"] = poLinesByItem[line["invoice_item_id"]]
		if line["po_lines"] == nil {
			line["po_lines"] = []map[string]any{}
		}
	}
	utils.OK(c, lines)
}

func (h *Handler) MatchPO(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		PurchaseOrderID string `json:"purchase_order_id"`
		Action          string `json:"action"`
		Note            string `json:"note"`
	}
	if c.ShouldBindJSON(&b) != nil || (strings.ToUpper(b.Action) != "CLEAR" && !utils.ValidUUID(b.PurchaseOrderID)) {
		utils.Bad(c, "purchase_order_id and action (CONFIRM, REJECT, CLEAR) are required")
		return
	}
	if err := h.Svc.SetPOMatch(c, h.DB, id, b.PurchaseOrderID, auth.CurrentUserID(c), b.Action, b.Note); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"saved": true})
}

func (h *Handler) Approve(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := h.Svc.Approve(c, h.DB, id, auth.CurrentUserID(c), b.Note); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_status": "APPROVED"})
}

func (h *Handler) Reject(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := h.Svc.Reject(c, h.DB, id, auth.CurrentUserID(c), b.Reason); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_status": "REJECTED"})
}

func (h *Handler) Override(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := h.Svc.Override(c, h.DB, id, auth.CurrentUserID(c), b.TargetStatus, b.Reason); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_status": strings.ToUpper(b.TargetStatus)})
}

func (h *Handler) ReturnToClerk(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		Note           string `json:"note"`
		AssigneeUserID string `json:"assignee_user_id"`
	}
	c.ShouldBindJSON(&b)
	if b.AssigneeUserID != "" && !utils.ValidUUID(b.AssigneeUserID) {
		utils.Bad(c, "invalid assignee")
		return
	}
	if err := h.Svc.ReturnToClerk(c, h.DB, id, auth.CurrentUserID(c), b.Note, b.AssigneeUserID); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_status": "RETURNED"})
}

func (h *Handler) Resubmit(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		Note string `json:"note"`
	}
	c.ShouldBindJSON(&b)
	if err := h.Svc.Resubmit(c, h.DB, id, auth.CurrentUserID(c), b.Note); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"resubmitted": true})
}

func (h *Handler) SubmitForApproval(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		Recommendation string `json:"recommendation"`
		Note           string `json:"note"`
	}
	c.ShouldBindJSON(&b)
	if err := h.Svc.SubmitForApproval(c, h.DB, id, auth.CurrentUserID(c), b.Recommendation, b.Note); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_status": "AWAITING_APPROVAL"})
}
