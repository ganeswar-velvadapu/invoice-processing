package handlers

// Tolerance rules (admin).

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/utils"
)

func (h *Handler) ListTolerances(c *gin.Context) {
	utils.SendRows(c, h.DB, `SELECT r.*, u.email AS created_by FROM tolerance_rules r
		LEFT JOIN users u ON u.user_id=r.created_by_user_id ORDER BY r.version DESC`)
}

func (h *Handler) CreateTolerance(c *gin.Context) {
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
		utils.Bad(c, "invalid tolerance values")
		return
	}
	tx, err := h.DB.BeginTx(c, nil)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	defer tx.Rollback()
	var ver int
	tx.QueryRowContext(c, `SELECT COALESCE(max(version),0)+1 FROM tolerance_rules`).Scan(&ver)
	if _, err := tx.ExecContext(c, `UPDATE tolerance_rules SET is_active=false WHERE is_active`); err != nil {
		utils.Fail(c, err)
		return
	}
	if _, err := tx.ExecContext(c, `INSERT INTO tolerance_rules (version, price_tolerance_pct, price_tolerance_abs, quantity_tolerance_pct, tax_rounding_abs,
		date_tolerance_days, min_extraction_confidence, is_active, created_by_user_id, note) VALUES ($1,$2,$3,$4,$5,$6,$7,true,$8,$9)`,
		ver, b.PriceTolerancePct, b.PriceToleranceAbs, b.QuantityTolerancePct, b.TaxRoundingAbs, b.DateToleranceDays, b.MinExtractionConfidence,
		auth.CurrentUserID(c), b.Note); err != nil {
		utils.Fail(c, err)
		return
	}
	if err := tx.Commit(); err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"version": ver, "note": "new invoices use this version; re-verify existing invoices to apply it"})
}
