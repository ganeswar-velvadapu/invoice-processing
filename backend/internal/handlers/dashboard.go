package handlers

// Dashboard numbers.

import (
	"github.com/gin-gonic/gin"

	"invoiceproc/internal/utils"
)

// Summary returns the dashboard numbers.
func (h *Handler) Summary(c *gin.Context) {
	counts := func(query string) map[string]int64 { // "label, count" rows → {"label": count}
		out := map[string]int64{}
		rows, err := h.DB.QueryContext(c, query)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int64
			if rows.Scan(&k, &n) == nil {
				out[k] = n
			}
		}
		return out
	}
	var awaitingAmount float64
	var processing int64
	h.DB.QueryRowContext(c, `SELECT COALESCE(sum(grand_total),0) FROM invoices WHERE invoice_status='READY_FOR_VERIFICATION'`).Scan(&awaitingAmount)
	h.DB.QueryRowContext(c, `SELECT (SELECT count(*) FROM documents WHERE processing_status IN ('RECEIVED','CLASSIFICATION_PENDING','EXTRACTION_PENDING'))
		+ (SELECT count(*) FROM invoices WHERE verify_requested_at IS NOT NULL)`).Scan(&processing)
	events, err := utils.Rows(c, h.DB, `SELECT e.event_type, e.event_description, e.created_at, e.invoice_id, e.document_id,
		e.actor_type, u.email AS actor_email FROM invoice_processing_events e LEFT JOIN users u ON u.user_id=e.actor_id
		ORDER BY e.created_at DESC LIMIT 15`)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{
		"invoices_by_status":          counts(`SELECT invoice_status, count(*) FROM invoices GROUP BY 1`),
		"invoices_by_decision":        counts(`SELECT COALESCE(decision,'NONE'), count(*) FROM invoices GROUP BY 1`),
		"documents_by_status":         counts(`SELECT processing_status, count(*) FROM documents GROUP BY 1`),
		"documents_by_type":           counts(`SELECT document_type, count(*) FROM documents GROUP BY 1`),
		"open_exceptions_by_severity": counts(`SELECT severity, count(*) FROM invoice_exceptions WHERE exception_status IN ('OPEN','ASSIGNED') GROUP BY 1`),
		"open_exceptions_by_code":     counts(`SELECT exception_code, count(*) FROM invoice_exceptions WHERE exception_status IN ('OPEN','ASSIGNED') GROUP BY 1`),
		"awaiting_approval_amount":    awaitingAmount,
		"processing":                  processing,
		"recent_events":               events,
	})
}
