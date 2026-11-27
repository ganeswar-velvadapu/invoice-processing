package handlers

// Vendor master data.

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/service"
	"invoiceproc/internal/utils"
)

func (h *Handler) ListVendors(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT v.*, (SELECT count(*) FROM invoices i WHERE i.vendor_id=v.vendor_id) AS invoice_count,
		(SELECT count(*) FROM purchase_orders p WHERE p.vendor_id=v.vendor_id) AS po_count,
		(SELECT field_patterns FROM vendor_extraction_profiles x WHERE x.vendor_id=v.vendor_id AND x.document_type='INVOICE') AS invoice_profile
		FROM vendors v WHERE ($1='' OR v.legal_name ILIKE '%'||$1||'%' OR v.gstin ILIKE '%'||$1||'%') ORDER BY v.legal_name`, c.Query("q"))
}

func (h *Handler) CreateVendor(c *gin.Context) {
	var v service.VendorInput
	if c.ShouldBindJSON(&v) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	res, err := h.Svc.Import(c, h.DB, service.ProcurementImport{Vendors: []service.VendorInput{v}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (h *Handler) SetVendorProfile(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		DocumentType  string            `json:"document_type"`
		FieldPatterns map[string]string `json:"field_patterns"`
	}
	if c.ShouldBindJSON(&b) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	if b.DocumentType == "" {
		b.DocumentType = "INVOICE"
	}
	for k, p := range b.FieldPatterns {
		if err := validatePattern(p); err != nil {
			utils.Bad(c, "pattern for "+k+": "+err.Error())
			return
		}
	}
	pj, _ := json.Marshal(b.FieldPatterns)
	if _, err := h.DB.ExecContext(c, `INSERT INTO vendor_extraction_profiles (vendor_id, document_type, field_patterns) VALUES ($1,$2,$3::jsonb)
		ON CONFLICT (vendor_id, document_type) DO UPDATE SET field_patterns=EXCLUDED.field_patterns, is_active=true`, id, b.DocumentType, string(pj)); err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"saved": true})
}

// validatePattern makes sure a vendor profile regex compiles and has exactly one capture group.
// Go's RE2 engine has linear-time matching, so user patterns cannot cause catastrophic backtracking.
func validatePattern(p string) error {
	if len(p) > 500 {
		return errors.New("pattern too long")
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return err
	}
	if re.NumSubexp() != 1 {
		return errors.New("pattern must contain exactly one capture group")
	}
	return nil
}
