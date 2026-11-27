package handlers

// Exceptions raised by the invoice checks.

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/utils"
)

func (h *Handler) ListExceptions(c *gin.Context) {
	status := c.DefaultQuery("status", "OPEN,ASSIGNED")
	utils.SendPage(c, h.DB, `SELECT x.*, i.invoice_number, i.seller_name, i.grand_total, i.invoice_status, ua.email AS assigned_to
		FROM invoice_exceptions x JOIN invoices i USING (invoice_id) LEFT JOIN users ua ON ua.user_id=x.assigned_to_user_id
		WHERE x.exception_status = ANY(string_to_array($1, ',')) AND ($2='' OR x.severity=$2) AND ($3='' OR x.exception_code=$3)
		ORDER BY CASE x.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END, x.created_at`,
		status, c.Query("severity"), c.Query("code"))
}

func (h *Handler) UpdateException(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		Action         string `json:"action"`
		Note           string `json:"note"`
		AssigneeUserID string `json:"assignee_user_id"`
	}
	if c.ShouldBindJSON(&b) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	if strings.ToUpper(b.Action) == "WAIVE" && !auth.HasRole(c, auth.RoleManager, auth.RoleAdmin) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "waiving an exception requires AP_MANAGER"})
		return
	}
	if b.AssigneeUserID != "" && !utils.ValidUUID(b.AssigneeUserID) {
		utils.Bad(c, "invalid assignee")
		return
	}
	inv, err := h.Svc.UpdateException(c, h.DB, id, auth.CurrentUserID(c), b.Action, b.Note, b.AssigneeUserID)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"invoice_id": inv})
}
