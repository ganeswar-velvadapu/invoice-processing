package handlers

// Email ingestion status and received emails.

import (
	"github.com/gin-gonic/gin"

	"invoiceproc/internal/utils"
)

func (h *Handler) EmailStatus(c *gin.Context) {
	st, err := h.Svc.EmailStatus(c, h.DB)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, st)
}

func (h *Handler) ListEmails(c *gin.Context) {
	emails, total, limit, offset, err := utils.Page(c, h.DB, `SELECT email_message_id, from_address, from_name, subject, sent_at, received_at,
		attachments_found, documents_created, status, note FROM email_messages ORDER BY received_at DESC`)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	// the documents each email created, in one query for the whole page
	ids := []string{}
	for _, e := range emails {
		ids = append(ids, e["email_message_id"].(string))
	}
	docs, err := utils.Rows(c, h.DB, `SELECT d.email_message_id, d.document_id, d.original_filename, d.document_type, d.processing_status,
		(SELECT invoice_id FROM invoices i WHERE i.document_id=d.document_id) AS invoice_id
		FROM documents d WHERE d.email_message_id = ANY($1::uuid[]) ORDER BY d.created_at`, ids)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	docsByEmail := map[any][]map[string]any{}
	for _, d := range docs {
		key := d["email_message_id"]
		delete(d, "email_message_id")
		docsByEmail[key] = append(docsByEmail[key], d)
	}
	for _, e := range emails {
		e["documents"] = docsByEmail[e["email_message_id"]]
		if e["documents"] == nil {
			e["documents"] = []map[string]any{}
		}
	}
	utils.OK(c, gin.H{"items": emails, "total": total, "limit": limit, "offset": offset})
}
