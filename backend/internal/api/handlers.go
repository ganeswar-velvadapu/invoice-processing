package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/parse"
	"invoiceproc/internal/service"
)

// ---------------------------------------------------------------- auth & users

func (a *API) login(c *gin.Context) {
	var body struct{ Email, Password string }
	if c.ShouldBindJSON(&body) != nil {
		bad(c, "email and password required")
		return
	}
	tok, u, err := a.Auth.Login(c, body.Email, body.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"token": tok, "user": u})
}

func (a *API) me(c *gin.Context) {
	a.rawJSON(c, `SELECT row_to_json(u) FROM (SELECT user_id, email, full_name, role, is_active FROM users WHERE user_id=$1) u`, auth.CurrentUserID(c))
}

func (a *API) listUsers(c *gin.Context) {
	a.list(c, `SELECT user_id, email, full_name, role, is_active, created_at FROM users ORDER BY email`)
}

func (a *API) createUser(c *gin.Context) {
	var b struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if c.ShouldBindJSON(&b) != nil || !strings.Contains(b.Email, "@") || len(b.Password) < 8 || !auth.ValidRoles[b.Role] {
		bad(c, "email, password (min 8 chars) and a valid role (AP_CLERK, AP_MANAGER, ADMIN) are required")
		return
	}
	h, err := auth.HashPassword(b.Password)
	if err != nil {
		a.fail(c, err)
		return
	}
	var id string
	if err := a.DB.QueryRowContext(c, `INSERT INTO users (email, full_name, password_hash, role) VALUES (lower($1),$2,$3,$4)
		ON CONFLICT (email) DO NOTHING RETURNING user_id`, strings.TrimSpace(b.Email), b.FullName, h, b.Role).Scan(&id); err != nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "a user with that email already exists"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user_id": id})
}

func (a *API) updateUser(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
		Password *string `json:"password"`
		FullName *string `json:"full_name"`
	}
	if c.ShouldBindJSON(&b) != nil {
		bad(c, "invalid body")
		return
	}
	if b.Role != nil && !auth.ValidRoles[*b.Role] {
		bad(c, "invalid role")
		return
	}
	if id == auth.CurrentUserID(c) && ((b.IsActive != nil && !*b.IsActive) || (b.Role != nil && *b.Role != auth.RoleAdmin)) {
		bad(c, "you cannot deactivate or demote yourself")
		return
	}
	var hash *string
	if b.Password != nil {
		if len(*b.Password) < 8 {
			bad(c, "password must be at least 8 characters")
			return
		}
		h, _ := auth.HashPassword(*b.Password)
		hash = &h
	}
	r, err := a.DB.ExecContext(c, `UPDATE users SET role=COALESCE($2,role), is_active=COALESCE($3,is_active), password_hash=COALESCE($4,password_hash),
		full_name=COALESCE($5,full_name), updated_at=now() WHERE user_id=$1`, id, b.Role, b.IsActive, hash, b.FullName)
	if err != nil {
		a.fail(c, err)
		return
	}
	if n, _ := r.RowsAffected(); n == 0 {
		a.fail(c, service.ErrNotFound)
		return
	}
	ok(c, gin.H{"updated": true})
}

// ---------------------------------------------------------------- dashboard

func (a *API) summary(c *gin.Context) {
	a.rawJSON(c, `SELECT json_build_object(
		'invoices_by_status', (SELECT COALESCE(json_object_agg(invoice_status, n), '{}') FROM (SELECT invoice_status, count(*) n FROM invoices GROUP BY 1) s),
		'invoices_by_decision', (SELECT COALESCE(json_object_agg(COALESCE(decision,'NONE'), n), '{}') FROM (SELECT decision, count(*) n FROM invoices GROUP BY 1) s),
		'documents_by_status', (SELECT COALESCE(json_object_agg(processing_status, n), '{}') FROM (SELECT processing_status, count(*) n FROM documents GROUP BY 1) s),
		'documents_by_type', (SELECT COALESCE(json_object_agg(document_type, n), '{}') FROM (SELECT document_type, count(*) n FROM documents GROUP BY 1) s),
		'open_exceptions_by_severity', (SELECT COALESCE(json_object_agg(severity, n), '{}') FROM (SELECT severity, count(*) n FROM invoice_exceptions WHERE exception_status IN ('OPEN','ASSIGNED') GROUP BY 1) s),
		'open_exceptions_by_code', (SELECT COALESCE(json_object_agg(exception_code, n), '{}') FROM (SELECT exception_code, count(*) n FROM invoice_exceptions WHERE exception_status IN ('OPEN','ASSIGNED') GROUP BY 1) s),
		'awaiting_approval_amount', (SELECT COALESCE(sum(grand_total),0) FROM invoices WHERE invoice_status='READY_FOR_VERIFICATION'),
		'jobs', (SELECT COALESCE(json_object_agg(job_status, n), '{}') FROM (SELECT job_status, count(*) n FROM processing_jobs GROUP BY 1) s),
		'recent_events', (SELECT COALESCE(json_agg(e), '[]') FROM (SELECT e.event_type, e.event_description, e.created_at, e.invoice_id, e.document_id,
			e.actor_type, u.email AS actor_email FROM invoice_processing_events e LEFT JOIN users u ON u.user_id=e.actor_id ORDER BY e.created_at DESC LIMIT 15) e)
	)`)
}

// ---------------------------------------------------------------- documents

func (a *API) uploadDocument(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		bad(c, "multipart field 'file' is required")
		return
	}
	f, err := fh.Open()
	if err != nil {
		a.fail(c, err)
		return
	}
	defer f.Close()
	declared := strings.ToUpper(c.PostForm("document_type"))
	if (declared == "PO" || declared == "GRN") && !auth.HasRole(c, auth.RoleManager) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "purchase orders and goods receipts are master data: only an AP manager or admin can upload them"})
		return
	}
	res, err := a.Svc.Ingest(c, service.IngestRequest{Reader: f, OriginalFilename: filepath.Base(fh.Filename),
		DeclaredType: declared, SourceType: "MANUAL", UserID: auth.CurrentUserID(c)})
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (a *API) listDocuments(c *gin.Context) {
	a.list(c, `SELECT d.document_id, d.document_type, d.declared_document_type, d.classification_confidence, d.original_filename, d.mime_type,
		d.file_size_bytes, d.sha256_hash, d.duplicate_of_document_id, d.source_type, d.received_at, d.processing_status, d.processing_stage,
		d.processing_error, d.page_count, u.email AS uploaded_by, em.from_address AS email_from, em.subject AS email_subject,
		(SELECT invoice_id FROM invoices i WHERE i.document_id=d.document_id) AS invoice_id,
		(SELECT purchase_order_id FROM purchase_orders p WHERE p.document_id=d.document_id LIMIT 1) AS purchase_order_id,
		(SELECT goods_receipt_note_id FROM goods_receipt_notes g WHERE g.document_id=d.document_id LIMIT 1) AS goods_receipt_note_id,
		(SELECT overall_confidence FROM document_extractions e WHERE e.document_id=d.document_id ORDER BY version DESC LIMIT 1) AS extraction_confidence
		FROM documents d LEFT JOIN users u ON u.user_id=d.uploaded_by_user_id
		LEFT JOIN email_messages em ON em.email_message_id=d.email_message_id
		WHERE ($1='' OR d.processing_status=$1) AND ($2='' OR d.document_type=$2) AND ($3='' OR d.original_filename ILIKE '%'||$3||'%')
		ORDER BY d.received_at DESC`, c.Query("status"), c.Query("type"), c.Query("q"))
}

func (a *API) getDocument(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT row_to_json(x) FROM (SELECT d.*, u.email AS uploaded_by,
		em.from_address AS email_from, em.from_name AS email_from_name, em.subject AS email_subject, em.sent_at AS email_sent_at,
		(SELECT invoice_id FROM invoices i WHERE i.document_id=d.document_id) AS invoice_id,
		(SELECT purchase_order_id FROM purchase_orders p WHERE p.document_id=d.document_id LIMIT 1) AS purchase_order_id,
		(SELECT goods_receipt_note_id FROM goods_receipt_notes g WHERE g.document_id=d.document_id LIMIT 1) AS goods_receipt_note_id,
		(SELECT COALESCE(json_agg(e ORDER BY e.created_at), '[]') FROM (SELECT ev.event_type, ev.event_description, ev.created_at, ev.actor_type,
			us.email AS actor_email, ev.event_data FROM invoice_processing_events ev LEFT JOIN users us ON us.user_id=ev.actor_id
			WHERE ev.document_id=d.document_id) e) AS events
		FROM documents d LEFT JOIN users u ON u.user_id=d.uploaded_by_user_id
		LEFT JOIN email_messages em ON em.email_message_id=d.email_message_id WHERE d.document_id=$1) x`, id)
}

func (a *API) documentFile(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var rel, mime, name string
	if err := a.DB.QueryRowContext(c, `SELECT storage_path, mime_type, original_filename FROM documents WHERE document_id=$1`, id).Scan(&rel, &mime, &name); err != nil {
		a.fail(c, err)
		return
	}
	f, err := a.Svc.Store.Open(rel)
	if err != nil {
		if os.IsNotExist(err) {
			c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": "original file is missing from storage"})
			return
		}
		a.fail(c, err)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	c.Header("Content-Type", mime)
	c.Header("Content-Disposition", `inline; filename="`+strings.NewReplacer(`"`, "", "\n", "", "\r", "").Replace(name)+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=300")
	http.ServeContent(c.Writer, c.Request, "", st.ModTime(), f)
}

func (a *API) reprocess(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		DocumentType string `json:"document_type"`
	}
	c.ShouldBindJSON(&b)
	if err := a.Svc.Reprocess(c, id, b.DocumentType, auth.CurrentUserID(c)); err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

func (a *API) getExtraction(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT json_build_object(
		'document_id', $1::uuid,
		'document_type', (SELECT document_type FROM documents WHERE document_id=$1),
		'latest', (SELECT row_to_json(e) FROM (SELECT document_extraction_id, version, extraction_method, extraction_model, raw_text, extracted_data,
			overall_confidence, ocr_mean_confidence, low_confidence_fields, created_at, (SELECT email FROM users WHERE user_id=created_by_user_id) AS created_by
			FROM document_extractions WHERE document_id=$1 ORDER BY version DESC LIMIT 1) e),
		'versions', (SELECT COALESCE(json_agg(v ORDER BY v.version DESC), '[]') FROM (SELECT version, extraction_method, extraction_model, overall_confidence, created_at,
			(SELECT email FROM users WHERE user_id=created_by_user_id) AS created_by FROM document_extractions WHERE document_id=$1) v)
	) WHERE EXISTS (SELECT 1 FROM documents WHERE document_id=$1)`, id)
}

func (a *API) correctExtraction(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var d parse.Data
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 2<<20)).Decode(&d); err != nil {
		bad(c, "invalid extraction JSON: "+err.Error())
		return
	}
	if err := a.Svc.CorrectExtraction(c, id, &d, auth.CurrentUserID(c)); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"saved": true})
}

// ---------------------------------------------------------------- vendors & procurement

func (a *API) listVendors(c *gin.Context) {
	a.list(c, `SELECT v.*, (SELECT count(*) FROM invoices i WHERE i.vendor_id=v.vendor_id) AS invoice_count,
		(SELECT count(*) FROM purchase_orders p WHERE p.vendor_id=v.vendor_id) AS po_count,
		(SELECT field_patterns FROM vendor_extraction_profiles x WHERE x.vendor_id=v.vendor_id AND x.document_type='INVOICE') AS invoice_profile
		FROM vendors v WHERE ($1='' OR v.legal_name ILIKE '%'||$1||'%' OR v.gstin ILIKE '%'||$1||'%') ORDER BY v.legal_name`, c.Query("q"))
}

func (a *API) createVendor(c *gin.Context) {
	var v service.VendorInput
	if c.ShouldBindJSON(&v) != nil {
		bad(c, "invalid body")
		return
	}
	res, err := a.Svc.Import(c, service.ProcurementImport{Vendors: []service.VendorInput{v}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (a *API) setVendorProfile(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		DocumentType  string            `json:"document_type"`
		FieldPatterns map[string]string `json:"field_patterns"`
	}
	if c.ShouldBindJSON(&b) != nil {
		bad(c, "invalid body")
		return
	}
	if b.DocumentType == "" {
		b.DocumentType = "INVOICE"
	}
	for k, p := range b.FieldPatterns {
		if err := validatePattern(p); err != nil {
			bad(c, "pattern for "+k+": "+err.Error())
			return
		}
	}
	pj, _ := json.Marshal(b.FieldPatterns)
	if _, err := a.DB.ExecContext(c, `INSERT INTO vendor_extraction_profiles (vendor_id, document_type, field_patterns) VALUES ($1,$2,$3::jsonb)
		ON CONFLICT (vendor_id, document_type) DO UPDATE SET field_patterns=EXCLUDED.field_patterns, is_active=true`, id, b.DocumentType, string(pj)); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"saved": true})
}

func (a *API) listPOs(c *gin.Context) {
	a.list(c, `SELECT p.purchase_order_id, p.po_number, p.po_date, p.po_status, p.currency, p.total_amount, p.source, p.document_id,
		v.legal_name AS vendor_name, COALESCE(v.gstin, p.vendor_gstin) AS vendor_gstin,
		(SELECT count(*) FROM purchase_order_items i WHERE i.purchase_order_id=p.purchase_order_id) AS line_count,
		(SELECT count(*) FROM grn_po_matches g WHERE g.purchase_order_id=p.purchase_order_id) AS grn_count,
		(SELECT count(DISTINCT m.invoice_id) FROM invoice_po_matches m WHERE m.purchase_order_id=p.purchase_order_id AND m.match_status='CONFIRMED') AS invoice_count
		FROM purchase_orders p LEFT JOIN vendors v USING (vendor_id)
		WHERE ($1='' OR p.po_number ILIKE '%'||$1||'%' OR v.legal_name ILIKE '%'||$1||'%') AND ($2='' OR p.vendor_id::text=$2)
		ORDER BY p.po_date DESC NULLS LAST, p.po_number`, c.Query("q"), c.Query("vendor_id"))
}

func (a *API) getPO(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT row_to_json(x) FROM (SELECT p.*, v.legal_name AS vendor_name,
		(SELECT COALESCE(json_agg(i ORDER BY i.line_number), '[]') FROM (SELECT poi.*,
			COALESCE((SELECT sum(gri.quantity_received) FROM goods_receipt_items gri WHERE gri.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_received,
			COALESCE((SELECT sum(gri.quantity_accepted) FROM goods_receipt_items gri WHERE gri.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_accepted,
			COALESCE((SELECT sum(m.allocated_quantity) FROM invoice_po_item_matches m JOIN invoices iv USING (invoice_id)
			  WHERE m.purchase_order_item_id=poi.purchase_order_item_id AND iv.invoice_status<>'REJECTED' AND iv.duplicate_of_invoice_id IS NULL),0) AS quantity_invoiced
			FROM purchase_order_items poi WHERE poi.purchase_order_id=p.purchase_order_id) i) AS items,
		(SELECT COALESCE(json_agg(g), '[]') FROM (SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status FROM goods_receipt_notes g
			JOIN grn_po_matches m USING (goods_receipt_note_id) WHERE m.purchase_order_id=p.purchase_order_id ORDER BY g.receipt_date) g) AS goods_receipts,
		(SELECT COALESCE(json_agg(iv), '[]') FROM (SELECT iv.invoice_id, iv.invoice_number, iv.invoice_date, iv.grand_total, iv.invoice_status, iv.decision,
			m.match_method, m.match_status FROM invoice_po_matches m JOIN invoices iv USING (invoice_id) WHERE m.purchase_order_id=p.purchase_order_id
			ORDER BY iv.invoice_date) iv) AS invoices
		FROM purchase_orders p LEFT JOIN vendors v USING (vendor_id) WHERE p.purchase_order_id=$1) x`, id)
}

func (a *API) createPO(c *gin.Context) {
	var p service.POInput
	if c.ShouldBindJSON(&p) != nil {
		bad(c, "invalid body")
		return
	}
	res, err := a.Svc.Import(c, service.ProcurementImport{PurchaseOrders: []service.POInput{p}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (a *API) listGRNs(c *gin.Context) {
	a.list(c, `SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status, g.po_reference, g.purchase_order_id, g.source, g.document_id,
		p.po_number, v.legal_name AS vendor_name,
		(SELECT count(*) FROM goods_receipt_items i WHERE i.goods_receipt_note_id=g.goods_receipt_note_id) AS line_count,
		(SELECT count(*) FROM goods_receipt_items i WHERE i.goods_receipt_note_id=g.goods_receipt_note_id AND i.purchase_order_item_id IS NULL) AS unlinked_lines
		FROM goods_receipt_notes g LEFT JOIN purchase_orders p USING (purchase_order_id) LEFT JOIN vendors v ON v.vendor_id=g.vendor_id
		WHERE ($1='' OR g.grn_number ILIKE '%'||$1||'%' OR g.po_reference ILIKE '%'||$1||'%')
		ORDER BY g.receipt_date DESC NULLS LAST, g.grn_number`, c.Query("q"))
}

func (a *API) getGRN(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT row_to_json(x) FROM (SELECT g.*, p.po_number, v.legal_name AS vendor_name,
		(SELECT COALESCE(json_agg(i ORDER BY i.line_number), '[]') FROM (SELECT gi.*, poi.line_number AS po_line_number
			FROM goods_receipt_items gi LEFT JOIN purchase_order_items poi USING (purchase_order_item_id) WHERE gi.goods_receipt_note_id=g.goods_receipt_note_id) i) AS items
		FROM goods_receipt_notes g LEFT JOIN purchase_orders p USING (purchase_order_id) LEFT JOIN vendors v ON v.vendor_id=g.vendor_id
		WHERE g.goods_receipt_note_id=$1) x`, id)
}

func (a *API) createGRN(c *gin.Context) {
	var g service.GRNInput
	if c.ShouldBindJSON(&g) != nil {
		bad(c, "invalid body")
		return
	}
	res, err := a.Svc.Import(c, service.ProcurementImport{GoodsReceiptNotes: []service.GRNInput{g}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

// importProcurement accepts either a JSON body or a multipart CSV upload with ?kind=.
func (a *API) importProcurement(c *gin.Context) {
	var in service.ProcurementImport
	if strings.HasPrefix(c.ContentType(), "multipart/") {
		fh, err := c.FormFile("file")
		if err != nil {
			bad(c, "multipart field 'file' is required")
			return
		}
		f, err := fh.Open()
		if err != nil {
			a.fail(c, err)
			return
		}
		defer f.Close()
		raw := io.LimitReader(f, 10<<20)
		name := strings.ToLower(fh.Filename)
		switch {
		case strings.HasSuffix(name, ".json"):
			if err := json.NewDecoder(raw).Decode(&in); err != nil {
				bad(c, "invalid JSON: "+err.Error())
				return
			}
		case strings.HasSuffix(name, ".xlsx"):
			if in, err = service.ParseWorkbook(raw, c.PostForm("kind")); err != nil {
				a.fail(c, err)
				return
			}
		case strings.HasSuffix(name, ".xls"):
			bad(c, "old .xls files are not supported: save the workbook as .xlsx (Excel Workbook) or CSV")
			return
		default:
			if in, err = service.ParseCSV(c.PostForm("kind"), raw); err != nil {
				a.fail(c, err)
				return
			}
		}
	} else if err := json.NewDecoder(io.LimitReader(c.Request.Body, 10<<20)).Decode(&in); err != nil {
		bad(c, "invalid JSON: "+err.Error())
		return
	}
	res, err := a.Svc.Import(c, in, "IMPORT", auth.CurrentUserID(c))
	if err != nil {
		a.fail(c, err)
		return
	}
	ok(c, res)
}

// ---------------------------------------------------------------- invoices

func (a *API) listInvoices(c *gin.Context) {
	a.list(c, `SELECT i.invoice_id, i.document_id, i.invoice_number, i.invoice_date, i.due_date, i.seller_name, i.seller_gstin, i.grand_total,
		i.currency, i.invoice_status, i.decision, i.decision_summary, i.requires_human_review, i.extraction_confidence, i.duplicate_of_invoice_id,
		i.po_references, i.last_verified_at, i.created_at, v.legal_name AS vendor_name,
		(SELECT COALESCE(json_agg(p.po_number), '[]') FROM invoice_po_matches m JOIN purchase_orders p USING (purchase_order_id)
			WHERE m.invoice_id=i.invoice_id AND m.match_status='CONFIRMED') AS matched_pos,
		(SELECT count(*) FROM invoice_exceptions e WHERE e.invoice_id=i.invoice_id AND e.exception_status IN ('OPEN','ASSIGNED')) AS open_exceptions
		FROM invoices i LEFT JOIN vendors v USING (vendor_id)
		WHERE ($1='' OR i.invoice_status = ANY(string_to_array($1, ','))) AND ($2='' OR i.decision=$2)
		AND ($3='' OR i.invoice_number ILIKE '%'||$3||'%' OR i.seller_name ILIKE '%'||$3||'%' OR v.legal_name ILIKE '%'||$3||'%' OR i.seller_gstin ILIKE '%'||$3||'%')
		ORDER BY i.created_at DESC`, c.Query("status"), c.Query("decision"), c.Query("q"))
}

func (a *API) getInvoice(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT row_to_json(x) FROM (SELECT i.*, v.legal_name AS vendor_name, v.is_approved AS vendor_approved, v.vendor_status,
		d.original_filename, d.mime_type, d.processing_status AS document_status, d.duplicate_of_document_id,
		(SELECT invoice_number FROM invoices o WHERE o.invoice_id=i.duplicate_of_invoice_id) AS duplicate_of_invoice_number,
		(SELECT email FROM users WHERE user_id=i.returned_by_user_id) AS returned_by,
		(SELECT email FROM users WHERE user_id=i.submitted_by_user_id) AS submitted_by,
		(SELECT email FROM users WHERE user_id=i.assigned_to_user_id) AS assigned_to,
		(SELECT COALESCE(json_agg(it ORDER BY it.line_number), '[]') FROM invoice_items it WHERE it.invoice_id=i.invoice_id) AS items,
		(SELECT COALESCE(json_agg(m ORDER BY m.match_status, m.match_confidence DESC), '[]') FROM (SELECT m.*, p.po_number, p.po_date, p.po_status, p.total_amount,
			p.document_id AS po_document_id, p.source AS po_source,
			(SELECT email FROM users WHERE user_id=m.matched_by_user_id) AS matched_by
			FROM invoice_po_matches m JOIN purchase_orders p USING (purchase_order_id) WHERE m.invoice_id=i.invoice_id) m) AS po_matches,
		(SELECT COALESCE(json_agg(g), '[]') FROM (SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status, g.document_id, g.source
			FROM invoice_grn_matches m JOIN goods_receipt_notes g USING (goods_receipt_note_id) WHERE m.invoice_id=i.invoice_id) g) AS goods_receipts,
		(SELECT overall_confidence FROM document_extractions e WHERE e.document_id=i.document_id ORDER BY version DESC LIMIT 1) AS latest_extraction_confidence,
		(SELECT low_confidence_fields FROM document_extractions e WHERE e.document_id=i.document_id ORDER BY version DESC LIMIT 1) AS low_confidence_fields
		FROM invoices i JOIN documents d USING (document_id) LEFT JOIN vendors v ON v.vendor_id=i.vendor_id WHERE i.invoice_id=$1) x`, id)
}

func (a *API) verify(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	res, err := a.Svc.Verify(c, id, auth.CurrentUserID(c))
	if err != nil {
		a.fail(c, err)
		return
	}
	ok(c, res)
}

func (a *API) invoiceChecks(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	if c.Query("all") == "true" {
		a.rawJSON(c, `SELECT COALESCE(json_agg(k ORDER BY k.checked_at DESC), '[]') FROM invoice_checks k WHERE invoice_id=$1`, id)
		return
	}
	a.rawJSON(c, `SELECT COALESCE(json_agg(k ORDER BY CASE k.check_status WHEN 'FAIL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END, k.check_code), '[]')
		FROM invoice_checks k WHERE invoice_id=$1 AND verification_run_id = (SELECT verification_run_id FROM invoice_checks WHERE invoice_id=$1
		ORDER BY checked_at DESC LIMIT 1)`, id)
}

func (a *API) invoiceExceptions(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT COALESCE(json_agg(e ORDER BY e.created_at DESC), '[]') FROM (SELECT x.*, ua.email AS assigned_to, ur.email AS resolved_by
		FROM invoice_exceptions x LEFT JOIN users ua ON ua.user_id=x.assigned_to_user_id LEFT JOIN users ur ON ur.user_id=x.resolved_by_user_id
		WHERE x.invoice_id=$1) e`, id)
}

func (a *API) invoiceEvents(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT COALESCE(json_agg(e ORDER BY e.created_at DESC), '[]') FROM (SELECT ev.*, u.email AS actor_email FROM invoice_processing_events ev
		LEFT JOIN users u ON u.user_id=ev.actor_id WHERE ev.invoice_id=$1 OR ev.document_id=(SELECT document_id FROM invoices WHERE invoice_id=$1)) e`, id)
}

// comparison returns invoice lines side by side with their PO lines and goods receipts.
func (a *API) comparison(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	a.rawJSON(c, `SELECT COALESCE(json_agg(r ORDER BY r.line_number), '[]') FROM (
		SELECT it.invoice_item_id, it.line_number, it.item_code, it.description, it.hsn_sac, it.quantity, it.unit_of_measure, it.unit_price,
		  it.tax_rate, it.taxable_value, it.confidence,
		  (SELECT COALESCE(json_agg(json_build_object(
		     'po_number', p.po_number, 'purchase_order_id', p.purchase_order_id, 'po_line', poi.line_number, 'item_code', poi.item_code,
		     'description', poi.description, 'quantity_ordered', poi.quantity_ordered, 'unit_price', poi.unit_price, 'tax_rate', poi.tax_rate,
		     'allocated_quantity', m.allocated_quantity, 'match_basis', m.match_basis,
		     'quantity_received', COALESCE((SELECT sum(g.quantity_received) FROM goods_receipt_items g WHERE g.purchase_order_item_id=poi.purchase_order_item_id),0),
		     'quantity_accepted', COALESCE((SELECT sum(g.quantity_accepted) FROM goods_receipt_items g WHERE g.purchase_order_item_id=poi.purchase_order_item_id),0),
		     'quantity_invoiced_other', COALESCE((SELECT sum(o.allocated_quantity) FROM invoice_po_item_matches o JOIN invoices iv ON iv.invoice_id=o.invoice_id
		        WHERE o.purchase_order_item_id=poi.purchase_order_item_id AND o.invoice_id<>it.invoice_id AND iv.invoice_status<>'REJECTED'
		        AND iv.duplicate_of_invoice_id IS NULL),0),
		     'receipts', (SELECT COALESCE(json_agg(json_build_object('grn_number', gn.grn_number, 'receipt_date', gn.receipt_date,
		        'received', g.quantity_received, 'accepted', g.quantity_accepted, 'rejected', g.quantity_rejected)), '[]')
		        FROM goods_receipt_items g JOIN goods_receipt_notes gn USING (goods_receipt_note_id) WHERE g.purchase_order_item_id=poi.purchase_order_item_id)
		   )), '[]') FROM invoice_po_item_matches m JOIN purchase_order_items poi USING (purchase_order_item_id)
		   JOIN purchase_orders p ON p.purchase_order_id=poi.purchase_order_id WHERE m.invoice_item_id=it.invoice_item_id) AS po_lines
		FROM invoice_items it WHERE it.invoice_id=$1) r WHERE EXISTS (SELECT 1 FROM invoices WHERE invoice_id=$1)`, id)
}

func (a *API) poMatch(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		PurchaseOrderID string `json:"purchase_order_id"`
		Action          string `json:"action"`
		Note            string `json:"note"`
	}
	if c.ShouldBindJSON(&b) != nil || (strings.ToUpper(b.Action) != "CLEAR" && !validUUID(b.PurchaseOrderID)) {
		bad(c, "purchase_order_id and action (CONFIRM, REJECT, CLEAR) are required")
		return
	}
	if err := a.Svc.SetPOMatch(c, id, b.PurchaseOrderID, auth.CurrentUserID(c), b.Action, b.Note); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"saved": true})
}

type reasonBody struct {
	Reason       string `json:"reason"`
	Note         string `json:"note"`
	TargetStatus string `json:"target_status"`
}

func (a *API) approve(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := a.Svc.Approve(c, id, auth.CurrentUserID(c), b.Note); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_status": "APPROVED"})
}

func (a *API) reject(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := a.Svc.Reject(c, id, auth.CurrentUserID(c), b.Reason); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_status": "REJECTED"})
}

func (a *API) override(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b reasonBody
	c.ShouldBindJSON(&b)
	if err := a.Svc.Override(c, id, auth.CurrentUserID(c), b.TargetStatus, b.Reason); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_status": strings.ToUpper(b.TargetStatus)})
}

// ---------------------------------------------------------------- exceptions

func (a *API) listExceptions(c *gin.Context) {
	status := c.DefaultQuery("status", "OPEN,ASSIGNED")
	a.list(c, `SELECT x.*, i.invoice_number, i.seller_name, i.grand_total, i.invoice_status, ua.email AS assigned_to
		FROM invoice_exceptions x JOIN invoices i USING (invoice_id) LEFT JOIN users ua ON ua.user_id=x.assigned_to_user_id
		WHERE x.exception_status = ANY(string_to_array($1, ',')) AND ($2='' OR x.severity=$2) AND ($3='' OR x.exception_code=$3)
		ORDER BY CASE x.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END, x.created_at`,
		status, c.Query("severity"), c.Query("code"))
}

func (a *API) updateException(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		Action         string `json:"action"`
		Note           string `json:"note"`
		AssigneeUserID string `json:"assignee_user_id"`
	}
	if c.ShouldBindJSON(&b) != nil {
		bad(c, "invalid body")
		return
	}
	if strings.ToUpper(b.Action) == "WAIVE" && !auth.HasRole(c, auth.RoleManager) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "waiving an exception requires AP_MANAGER"})
		return
	}
	if b.AssigneeUserID != "" && !validUUID(b.AssigneeUserID) {
		bad(c, "invalid assignee")
		return
	}
	inv, err := a.Svc.UpdateException(c, id, auth.CurrentUserID(c), b.Action, b.Note, b.AssigneeUserID)
	if err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_id": inv})
}

// ---------------------------------------------------------------- tolerance rules

func (a *API) listTolerances(c *gin.Context) {
	a.rawJSON(c, `SELECT COALESCE(json_agg(t ORDER BY t.version DESC), '[]') FROM (SELECT r.*, u.email AS created_by FROM tolerance_rules r
		LEFT JOIN users u ON u.user_id=r.created_by_user_id) t`)
}

func (a *API) createTolerance(c *gin.Context) {
	var b struct {
		PriceTolerancePct       float64 `json:"price_tolerance_pct"`
		PriceToleranceAbs       float64 `json:"price_tolerance_abs"`
		QuantityTolerancePct    float64 `json:"quantity_tolerance_pct"`
		TaxRoundingAbs          float64 `json:"tax_rounding_abs"`
		DateToleranceDays       int     `json:"date_tolerance_days"`
		MinExtractionConfidence float64 `json:"min_extraction_confidence"`
		Note                    string  `json:"note"`
	}
	if c.ShouldBindJSON(&b) != nil || b.PriceTolerancePct < 0 || b.PriceToleranceAbs < 0 || b.QuantityTolerancePct < 0 || b.TaxRoundingAbs < 0 ||
		b.MinExtractionConfidence < 0 || b.MinExtractionConfidence > 1 || b.PriceTolerancePct > 50 || b.QuantityTolerancePct > 50 {
		bad(c, "invalid tolerance values")
		return
	}
	tx, err := a.DB.BeginTx(c, nil)
	if err != nil {
		a.fail(c, err)
		return
	}
	defer tx.Rollback()
	var ver int
	tx.QueryRowContext(c, `SELECT COALESCE(max(version),0)+1 FROM tolerance_rules`).Scan(&ver)
	if _, err := tx.ExecContext(c, `UPDATE tolerance_rules SET is_active=false WHERE is_active`); err != nil {
		a.fail(c, err)
		return
	}
	if _, err := tx.ExecContext(c, `INSERT INTO tolerance_rules (version, price_tolerance_pct, price_tolerance_abs, quantity_tolerance_pct, tax_rounding_abs,
		date_tolerance_days, min_extraction_confidence, is_active, created_by_user_id, note) VALUES ($1,$2,$3,$4,$5,$6,$7,true,$8,$9)`,
		ver, b.PriceTolerancePct, b.PriceToleranceAbs, b.QuantityTolerancePct, b.TaxRoundingAbs, b.DateToleranceDays, b.MinExtractionConfidence,
		auth.CurrentUserID(c), b.Note); err != nil {
		a.fail(c, err)
		return
	}
	if err := tx.Commit(); err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"version": ver, "note": "new invoices use this version; re-verify existing invoices to apply it"})
}

// importTemplate serves an Excel workbook (all kinds) or a CSV template for one kind.
func (a *API) importTemplate(c *gin.Context) {
	if c.Query("format") == "csv" {
		kind := c.Query("kind")
		b, err := service.TemplateCSV(kind)
		if err != nil {
			a.fail(c, err)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="`+kind+`_template.csv"`)
		c.Data(http.StatusOK, "text/csv; charset=utf-8", b)
		return
	}
	b, err := service.TemplateXLSX()
	if err != nil {
		a.fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="procurement_import_template.xlsx"`)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", b)
}

func (a *API) returnToClerk(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		Note           string `json:"note"`
		AssigneeUserID string `json:"assignee_user_id"`
	}
	c.ShouldBindJSON(&b)
	if b.AssigneeUserID != "" && !validUUID(b.AssigneeUserID) {
		bad(c, "invalid assignee")
		return
	}
	if err := a.Svc.ReturnToClerk(c, id, auth.CurrentUserID(c), b.Note, b.AssigneeUserID); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_status": "RETURNED"})
}

func (a *API) resubmit(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		Note string `json:"note"`
	}
	c.ShouldBindJSON(&b)
	if err := a.Svc.Resubmit(c, id, auth.CurrentUserID(c), b.Note); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"resubmitted": true})
}

func (a *API) submitForApproval(c *gin.Context) {
	id, okk := idParam(c)
	if !okk {
		return
	}
	var b struct {
		Recommendation string `json:"recommendation"`
		Note           string `json:"note"`
	}
	c.ShouldBindJSON(&b)
	if err := a.Svc.SubmitForApproval(c, id, auth.CurrentUserID(c), b.Recommendation, b.Note); err != nil {
		a.fail(c, err)
		return
	}
	ok(c, gin.H{"invoice_status": "AWAITING_APPROVAL"})
}

func (a *API) emailStatus(c *gin.Context) {
	st, err := a.Svc.EmailStatus(c)
	if err != nil {
		a.fail(c, err)
		return
	}
	ok(c, st)
}

func (a *API) listEmails(c *gin.Context) {
	a.list(c, `SELECT em.email_message_id, em.from_address, em.from_name, em.subject, em.sent_at, em.received_at, em.attachments_found,
		em.documents_created, em.status, em.note,
		(SELECT COALESCE(json_agg(json_build_object('document_id', d.document_id, 'original_filename', d.original_filename,
			'document_type', d.document_type, 'processing_status', d.processing_status,
			'invoice_id', (SELECT invoice_id FROM invoices i WHERE i.document_id=d.document_id)) ORDER BY d.created_at), '[]')
			FROM documents d WHERE d.email_message_id=em.email_message_id) AS documents
		FROM email_messages em ORDER BY em.received_at DESC`)
}
