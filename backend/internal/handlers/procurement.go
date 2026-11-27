package handlers

// Purchase orders, goods receipts and imports.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/service"
	"invoiceproc/internal/utils"
)

func (h *Handler) ListPOs(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT p.purchase_order_id, p.po_number, p.po_date, p.po_status, p.currency, p.total_amount, p.source, p.document_id,
		v.legal_name AS vendor_name, COALESCE(v.gstin, p.vendor_gstin) AS vendor_gstin,
		(SELECT count(*) FROM purchase_order_items i WHERE i.purchase_order_id=p.purchase_order_id) AS line_count,
		(SELECT count(*) FROM grn_po_matches g WHERE g.purchase_order_id=p.purchase_order_id) AS grn_count,
		(SELECT count(DISTINCT m.invoice_id) FROM invoice_po_matches m WHERE m.purchase_order_id=p.purchase_order_id AND m.match_status='CONFIRMED') AS invoice_count
		FROM purchase_orders p LEFT JOIN vendors v USING (vendor_id)
		WHERE ($1='' OR p.po_number ILIKE '%'||$1||'%' OR v.legal_name ILIKE '%'||$1||'%') AND ($2='' OR p.vendor_id::text=$2)
		ORDER BY p.po_date DESC NULLS LAST, p.po_number`, c.Query("q"), c.Query("vendor_id"))
}

func (h *Handler) GetPO(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	po, err := utils.Row(c, h.DB, `SELECT p.*, v.legal_name AS vendor_name FROM purchase_orders p
		LEFT JOIN vendors v USING (vendor_id) WHERE p.purchase_order_id=$1`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	items, err := utils.Rows(c, h.DB, `SELECT poi.*,
		COALESCE((SELECT sum(gri.quantity_received) FROM goods_receipt_items gri WHERE gri.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_received,
		COALESCE((SELECT sum(gri.quantity_accepted) FROM goods_receipt_items gri WHERE gri.purchase_order_item_id=poi.purchase_order_item_id),0) AS quantity_accepted,
		COALESCE((SELECT sum(m.allocated_quantity) FROM invoice_po_item_matches m JOIN invoices iv USING (invoice_id)
		  WHERE m.purchase_order_item_id=poi.purchase_order_item_id AND iv.invoice_status<>'REJECTED' AND iv.duplicate_of_invoice_id IS NULL),0) AS quantity_invoiced
		FROM purchase_order_items poi WHERE poi.purchase_order_id=$1 ORDER BY poi.line_number`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	receipts, err := utils.Rows(c, h.DB, `SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status FROM goods_receipt_notes g
		JOIN grn_po_matches m USING (goods_receipt_note_id) WHERE m.purchase_order_id=$1 ORDER BY g.receipt_date`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	invoices, err := utils.Rows(c, h.DB, `SELECT iv.invoice_id, iv.invoice_number, iv.invoice_date, iv.grand_total, iv.invoice_status, iv.decision,
		m.match_method, m.match_status FROM invoice_po_matches m JOIN invoices iv USING (invoice_id) WHERE m.purchase_order_id=$1
		ORDER BY iv.invoice_date`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	po["items"] = items
	po["goods_receipts"] = receipts
	po["invoices"] = invoices
	utils.OK(c, po)
}

func (h *Handler) CreatePO(c *gin.Context) {
	var p service.POInput
	if c.ShouldBindJSON(&p) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	res, err := h.Svc.Import(c, h.DB, service.ProcurementImport{PurchaseOrders: []service.POInput{p}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (h *Handler) ListGRNs(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT g.goods_receipt_note_id, g.grn_number, g.receipt_date, g.receipt_status, g.po_reference, g.purchase_order_id, g.source, g.document_id,
		p.po_number, v.legal_name AS vendor_name,
		(SELECT count(*) FROM goods_receipt_items i WHERE i.goods_receipt_note_id=g.goods_receipt_note_id) AS line_count,
		(SELECT count(*) FROM goods_receipt_items i WHERE i.goods_receipt_note_id=g.goods_receipt_note_id AND i.purchase_order_item_id IS NULL) AS unlinked_lines
		FROM goods_receipt_notes g LEFT JOIN purchase_orders p USING (purchase_order_id) LEFT JOIN vendors v ON v.vendor_id=g.vendor_id
		WHERE ($1='' OR g.grn_number ILIKE '%'||$1||'%' OR g.po_reference ILIKE '%'||$1||'%')
		ORDER BY g.receipt_date DESC NULLS LAST, g.grn_number`, c.Query("q"))
}

func (h *Handler) GetGRN(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	grn, err := utils.Row(c, h.DB, `SELECT g.*, p.po_number, v.legal_name AS vendor_name FROM goods_receipt_notes g
		LEFT JOIN purchase_orders p USING (purchase_order_id) LEFT JOIN vendors v ON v.vendor_id=g.vendor_id
		WHERE g.goods_receipt_note_id=$1`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	items, err := utils.Rows(c, h.DB, `SELECT gi.*, poi.line_number AS po_line_number FROM goods_receipt_items gi
		LEFT JOIN purchase_order_items poi USING (purchase_order_item_id) WHERE gi.goods_receipt_note_id=$1 ORDER BY gi.line_number`, id)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	grn["items"] = items
	utils.OK(c, grn)
}

func (h *Handler) CreateGRN(c *gin.Context) {
	var g service.GRNInput
	if c.ShouldBindJSON(&g) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	res, err := h.Svc.Import(c, h.DB, service.ProcurementImport{GoodsReceiptNotes: []service.GRNInput{g}}, "MANUAL", auth.CurrentUserID(c))
	if err != nil {
		utils.Fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

// importProcurement accepts either a JSON body or a multipart CSV upload with ?kind=.
func (h *Handler) ImportProcurement(c *gin.Context) {
	var in service.ProcurementImport
	if strings.HasPrefix(c.ContentType(), "multipart/") {
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
		raw := io.LimitReader(f, 10<<20)
		name := strings.ToLower(fh.Filename)
		switch {
		case strings.HasSuffix(name, ".json"):
			if err := json.NewDecoder(raw).Decode(&in); err != nil {
				utils.Bad(c, "invalid JSON: "+err.Error())
				return
			}
		case strings.HasSuffix(name, ".xlsx"):
			if in, err = service.ParseWorkbook(raw, c.PostForm("kind")); err != nil {
				utils.Fail(c, err)
				return
			}
		case strings.HasSuffix(name, ".xls"):
			utils.Bad(c, "old .xls files are not supported: save the workbook as .xlsx (Excel Workbook) or CSV")
			return
		default:
			if in, err = service.ParseCSV(c.PostForm("kind"), raw); err != nil {
				utils.Fail(c, err)
				return
			}
		}
	} else if err := json.NewDecoder(io.LimitReader(c.Request.Body, 10<<20)).Decode(&in); err != nil {
		utils.Bad(c, "invalid JSON: "+err.Error())
		return
	}
	res, err := h.Svc.Import(c, h.DB, in, "IMPORT", auth.CurrentUserID(c))
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, res)
}

// importTemplate serves an Excel workbook (all kinds) or a CSV template for one kind.
func (h *Handler) ImportTemplate(c *gin.Context) {
	if c.Query("format") == "csv" {
		kind := c.Query("kind")
		b, err := service.TemplateCSV(kind)
		if err != nil {
			utils.Fail(c, err)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="`+kind+`_template.csv"`)
		c.Data(http.StatusOK, "text/csv; charset=utf-8", b)
		return
	}
	b, err := service.TemplateXLSX()
	if err != nil {
		utils.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="procurement_import_template.xlsx"`)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", b)
}
