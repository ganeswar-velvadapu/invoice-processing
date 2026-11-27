package handlers

// Uploaded documents and what was read from them.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/parse"
	"invoiceproc/internal/service"
	"invoiceproc/internal/utils"
)

func (h *Handler) UploadDocument(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		utils.Bad(c, "multipart field 'file' is required")
		return
	}
	f, err := fh.Open()
	if err != nil {
		utils.Fail(c, err)
		return
	}
	defer f.Close()
	declared := strings.ToUpper(c.PostForm("document_type"))
	if (declared == "PO" || declared == "GRN") && !auth.HasRole(c, auth.RoleManager, auth.RoleAdmin) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "purchase orders and goods receipts are master data: only an AP manager or admin can upload them"})
		return
	}
	res, err := h.Svc.Ingest(c, h.DB, service.IngestRequest{Reader: f, OriginalFilename: filepath.Base(fh.Filename),
		DeclaredType: declared, SourceType: "MANUAL", UserID: auth.CurrentUserID(c)})
	if err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (h *Handler) ListDocuments(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT d.document_id, d.document_type, d.declared_document_type, d.classification_confidence, d.original_filename, d.mime_type,
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

func (h *Handler) GetDocument(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	doc, err := utils.Row(c, h.DB, `SELECT d.*, u.email AS uploaded_by,
		em.from_address AS email_from, em.from_name AS email_from_name, em.subject AS email_subject, em.sent_at AS email_sent_at,
		(SELECT invoice_id FROM invoices i WHERE i.document_id=d.document_id) AS invoice_id,
		(SELECT purchase_order_id FROM purchase_orders p WHERE p.document_id=d.document_id LIMIT 1) AS purchase_order_id,
		(SELECT goods_receipt_note_id FROM goods_receipt_notes g WHERE g.document_id=d.document_id LIMIT 1) AS goods_receipt_note_id
		FROM documents d LEFT JOIN users u ON u.user_id=d.uploaded_by_user_id
		LEFT JOIN email_messages em ON em.email_message_id=d.email_message_id WHERE d.document_id=$1`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	events, err := utils.Rows(c, h.DB, `SELECT ev.event_type, ev.event_description, ev.created_at, ev.actor_type,
		us.email AS actor_email, ev.event_data FROM invoice_processing_events ev LEFT JOIN users us ON us.user_id=ev.actor_id
		WHERE ev.document_id=$1 ORDER BY ev.created_at`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	doc["events"] = events
	utils.OK(c, doc)
}

func (h *Handler) DocumentFile(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var rel, mime, name string
	if err := h.DB.QueryRowContext(c, `SELECT storage_path, mime_type, original_filename FROM documents WHERE document_id=$1`, id).Scan(&rel, &mime, &name); err != nil {
		utils.Fail(c, err)
		return
	}
	f, err := h.Svc.Store.Open(rel)
	if err != nil {
		if os.IsNotExist(err) {
			c.AbortWithStatusJSON(http.StatusGone, gin.H{"error": "original file is missing from storage"})
			return
		}
		utils.Fail(c, err)
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

func (h *Handler) Reprocess(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		DocumentType string `json:"document_type"`
	}
	c.ShouldBindJSON(&b)
	if err := h.Svc.Reprocess(c, h.DB, id, b.DocumentType, auth.CurrentUserID(c)); err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

func (h *Handler) GetExtraction(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	doc, err := utils.Row(c, h.DB, `SELECT document_type FROM documents WHERE document_id=$1`, id)
	if err != nil {
		utils.Fail(c, err) // 404 for an unknown document
		return
	}
	versions, err := utils.Rows(c, h.DB, `SELECT version, extraction_method, extraction_model, overall_confidence, created_at,
		(SELECT email FROM users WHERE user_id=created_by_user_id) AS created_by
		FROM document_extractions WHERE document_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	var latest any // null until the document has been read
	if row, err := utils.Row(c, h.DB, `SELECT document_extraction_id, version, extraction_method, extraction_model, raw_text, extracted_data,
		overall_confidence, ocr_mean_confidence, low_confidence_fields, created_at, (SELECT email FROM users WHERE user_id=created_by_user_id) AS created_by
		FROM document_extractions WHERE document_id=$1 ORDER BY version DESC LIMIT 1`, id); err == nil {
		latest = row
	}
	utils.OK(c, gin.H{"document_id": id, "document_type": doc["document_type"], "latest": latest, "versions": versions})
}

func (h *Handler) CorrectExtraction(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var d parse.Data
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 2<<20)).Decode(&d); err != nil {
		utils.Bad(c, "invalid extraction JSON: "+err.Error())
		return
	}
	if err := h.Svc.CorrectExtraction(c, h.DB, id, &d, auth.CurrentUserID(c)); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"saved": true})
}
