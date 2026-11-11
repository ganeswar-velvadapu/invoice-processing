package service

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"invoiceproc/internal/parse"
	"invoiceproc/internal/util"
)

// Tabular import: CSV files and Excel workbooks become rows, and one mapping turns rows into the
// same VendorInput / POInput / GRNInput structs used by JSON imports and PDF extraction.
//
// Layout: one row per line item; PO/GRN header columns repeat on every row of the same document.
// Column names are matched case-insensitively with common aliases ("PO No", "Qty", "Rate", "UOM").

// Canonical columns per kind, in template order. Required ones are marked with *.
var importColumns = map[string][]string{
	"vendors": {"legal_name*", "gstin*", "address", "is_approved", "vendor_status"},
	"purchase_orders": {"po_number*", "vendor_gstin*", "po_date", "buyer_entity", "currency", "po_status",
		"line_number", "item_code", "description", "hsn_sac", "quantity_ordered*", "unit_of_measure", "unit_price*", "tax_rate"},
	"goods_receipt_notes": {"grn_number*", "po_number*", "vendor_gstin", "receipt_date",
		"line_number", "item_code", "description", "quantity_received*", "quantity_accepted", "quantity_rejected", "unit_of_measure"},
}

// Aliases seen in real ERP / spreadsheet exports (normalised: lower case, single spaces, no punctuation).
var columnAliases = map[string]string{
	"vendor name": "legal_name", "supplier name": "legal_name", "name": "legal_name", "vendor": "legal_name", "supplier": "legal_name",
	"gst no": "gstin", "gst number": "gstin", "gstin no": "gstin", "gstin uin": "gstin",
	"approved": "is_approved", "status": "vendor_status",
	"po no": "po_number", "po number": "po_number", "po": "po_number", "purchase order": "po_number", "purchase order no": "po_number",
	"purchase order number": "po_number", "order no": "po_number", "order number": "po_number", "po ref": "po_number", "po reference": "po_number",
	"vendor gstin": "vendor_gstin", "supplier gstin": "vendor_gstin", "vendor gst": "vendor_gstin",
	"po date": "po_date", "order date": "po_date", "date": "date",
	"buyer": "buyer_entity", "company": "buyer_entity", "entity": "buyer_entity",
	"line": "line_number", "line no": "line_number", "sl no": "line_number", "s no": "line_number", "sr no": "line_number", "sl": "line_number",
	"item": "item_code", "item code": "item_code", "sku": "item_code", "material code": "item_code", "part no": "item_code",
	"item description": "description", "item name": "description", "material description": "description", "particulars": "description",
	"hsn": "hsn_sac", "hsn code": "hsn_sac", "hsn sac": "hsn_sac", "sac": "hsn_sac",
	"qty": "quantity", "quantity": "quantity", "ordered qty": "quantity_ordered", "qty ordered": "quantity_ordered", "order qty": "quantity_ordered",
	"uom": "unit_of_measure", "unit": "unit_of_measure", "units": "unit_of_measure",
	"rate": "unit_price", "price": "unit_price", "unit rate": "unit_price", "unit cost": "unit_price",
	"gst": "tax_rate", "gst rate": "tax_rate", "gst %": "tax_rate", "gst percent": "tax_rate", "tax %": "tax_rate", "tax rate": "tax_rate",
	"grn no": "grn_number", "grn number": "grn_number", "grn": "grn_number", "receipt no": "grn_number", "mrn no": "grn_number",
	"grn date": "receipt_date", "received date": "receipt_date", "receipt date": "receipt_date",
	"received qty": "quantity_received", "qty received": "quantity_received", "rcvd qty": "quantity_received", "received": "quantity_received",
	"accepted qty": "quantity_accepted", "qty accepted": "quantity_accepted", "accepted": "quantity_accepted",
	"rejected qty": "quantity_rejected", "qty rejected": "quantity_rejected", "rejected": "quantity_rejected",
}

func normHeader(h string) string {
	h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))
	h = strings.NewReplacer("_", " ", "-", " ", ".", " ", "/", " ", "(", " ", ")", " ", "#", " no ", ":", " ").Replace(h)
	h = strings.Join(strings.Fields(h), " ")
	h = strings.ReplaceAll(h, "no no", "no")
	return h
}

// canonical maps a header to a canonical column for the kind ("" if unknown).
func canonical(kind, header string) string {
	n := normHeader(header)
	cols := importColumns[kind]
	for _, c := range cols {
		c = strings.TrimSuffix(c, "*")
		if n == strings.ReplaceAll(c, "_", " ") {
			return c
		}
	}
	a, ok := columnAliases[n]
	if !ok {
		return ""
	}
	// generic names resolve per kind
	switch a {
	case "quantity":
		if kind == "goods_receipt_notes" {
			return "quantity_received"
		}
		return "quantity_ordered"
	case "date":
		if kind == "goods_receipt_notes" {
			return "receipt_date"
		}
		return "po_date"
	case "legal_name":
		if kind != "vendors" {
			return ""
		}
	case "vendor_status", "is_approved":
		if kind != "vendors" {
			return ""
		}
	case "gstin":
		if kind != "vendors" {
			return "vendor_gstin"
		}
	}
	for _, c := range cols {
		if strings.TrimSuffix(c, "*") == a {
			return a
		}
	}
	return ""
}

// ImportRowError collects row-level problems so nothing partial is saved.
type ImportRowError struct{ Problems []string }

func (e *ImportRowError) Error() string {
	shown := e.Problems
	if len(shown) > 15 {
		shown = append(shown[:15:15], fmt.Sprintf("… and %d more", len(e.Problems)-15))
	}
	return "the file has errors, nothing was saved:\n- " + strings.Join(shown, "\n- ")
}

// ParseRows maps header + data rows of one kind into a ProcurementImport.
func ParseRows(kind, where string, rows [][]string, dateSerial bool) (ProcurementImport, error) {
	var out ProcurementImport
	cols, ok := importColumns[kind]
	if !ok {
		return out, userErr(ErrInvalid, "kind must be vendors, purchase_orders or goods_receipt_notes")
	}
	// skip leading blank rows; first non-blank row is the header
	for len(rows) > 0 && blankRow(rows[0]) {
		rows = rows[1:]
	}
	if len(rows) < 2 {
		return out, userErr(ErrInvalid, where+": no data rows")
	}
	idx := map[string]int{}
	var unknown []string
	for i, h := range rows[0] {
		if strings.TrimSpace(h) == "" {
			continue
		}
		if c := canonical(kind, h); c != "" {
			if _, dup := idx[c]; !dup {
				idx[c] = i
			}
		} else {
			unknown = append(unknown, h)
		}
	}
	var missing []string
	for _, c := range cols {
		if strings.HasSuffix(c, "*") {
			if _, ok := idx[strings.TrimSuffix(c, "*")]; !ok {
				missing = append(missing, strings.TrimSuffix(c, "*"))
			}
		}
	}
	if len(missing) > 0 {
		msg := fmt.Sprintf("%s: missing required column(s) %s", where, strings.Join(missing, ", "))
		if len(unknown) > 0 {
			msg += fmt.Sprintf(" (unrecognised columns: %s)", strings.Join(unknown, ", "))
		}
		return out, userErr(ErrInvalid, msg)
	}
	errs := &ImportRowError{}
	get := func(row []string, k string) string {
		if i, ok := idx[k]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	num := func(row []string, k string, rowNo int, required bool) float64 {
		v := strings.NewReplacer(",", "", "₹", "", "%", "").Replace(get(row, k))
		if v == "" {
			if required {
				errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: %s is empty", where, rowNo, k))
			}
			return 0
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(f) || f < 0 {
			errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: %s %q is not a valid number", where, rowNo, k, get(row, k)))
		}
		return f
	}
	date := func(row []string, k string, rowNo int) string {
		v := get(row, k)
		if v == "" {
			return ""
		}
		if dateSerial { // Excel stores dates as day counts since 1899-12-30
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 20000 && f < 80000 {
				return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(f)).Format("2006-01-02")
			}
		}
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return t.Format("2006-01-02")
		}
		if t, ok := parse.ParseDate(v); ok {
			return t.Format("2006-01-02")
		}
		errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: %s %q is not a date (use YYYY-MM-DD or DD/MM/YYYY)", where, rowNo, k, v))
		return ""
	}
	line := func(row []string, rowNo, fallback int) int {
		if v := get(row, "line_number"); v != "" {
			if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
				return int(n)
			}
			errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: line_number %q is not a number", where, rowNo, v))
		}
		return fallback
	}

	byNum := map[string]int{}
	for r, row := range rows[1:] {
		rowNo := r + 2 // 1-based, after the header
		if blankRow(row) {
			continue
		}
		switch kind {
		case "vendors":
			name, g := get(row, "legal_name"), get(row, "gstin")
			if name == "" {
				errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: legal_name is empty", where, rowNo))
			}
			ap := strings.ToLower(get(row, "is_approved"))
			out.Vendors = append(out.Vendors, VendorInput{LegalName: name, GSTIN: g, Address: get(row, "address"),
				IsApproved: ap == "" || ap == "true" || ap == "yes" || ap == "1" || ap == "y", VendorStatus: strings.ToUpper(get(row, "vendor_status"))})
		case "purchase_orders":
			n := util.NormalizeRef(get(row, "po_number"))
			if n == "" {
				errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: po_number is empty", where, rowNo))
				continue
			}
			i, ok := byNum[n]
			if !ok {
				out.PurchaseOrders = append(out.PurchaseOrders, POInput{PONumber: get(row, "po_number"), VendorGSTIN: get(row, "vendor_gstin"),
					PODate: date(row, "po_date", rowNo), BuyerEntity: get(row, "buyer_entity"), Currency: strings.ToUpper(get(row, "currency")),
					POStatus: strings.ToUpper(get(row, "po_status"))})
				i = len(out.PurchaseOrders) - 1
				byNum[n] = i
			}
			po := &out.PurchaseOrders[i]
			po.Items = append(po.Items, POItemInput{LineNumber: line(row, rowNo, len(po.Items)+1), ItemCode: get(row, "item_code"),
				Description: get(row, "description"), HSNSAC: get(row, "hsn_sac"), QuantityOrdered: num(row, "quantity_ordered", rowNo, true),
				UnitOfMeasure: get(row, "unit_of_measure"), UnitPrice: num(row, "unit_price", rowNo, true), TaxRate: num(row, "tax_rate", rowNo, false)})
		case "goods_receipt_notes":
			n := util.NormalizeRef(get(row, "grn_number"))
			if n == "" {
				errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: grn_number is empty", where, rowNo))
				continue
			}
			i, ok := byNum[n]
			if !ok {
				out.GoodsReceiptNotes = append(out.GoodsReceiptNotes, GRNInput{GRNNumber: get(row, "grn_number"), PONumber: get(row, "po_number"),
					VendorGSTIN: get(row, "vendor_gstin"), ReceiptDate: date(row, "receipt_date", rowNo)})
				i = len(out.GoodsReceiptNotes) - 1
				byNum[n] = i
			}
			g := &out.GoodsReceiptNotes[i]
			it := GRNItemInput{LineNumber: line(row, rowNo, len(g.Items)+1), ItemCode: get(row, "item_code"), Description: get(row, "description"),
				QuantityReceived: num(row, "quantity_received", rowNo, true), QuantityAccepted: num(row, "quantity_accepted", rowNo, false),
				QuantityRejected: num(row, "quantity_rejected", rowNo, false), UnitOfMeasure: get(row, "unit_of_measure")}
			if get(row, "quantity_accepted") == "" { // nothing rejected unless stated
				it.QuantityAccepted = it.QuantityReceived - it.QuantityRejected
			}
			if it.QuantityAccepted+it.QuantityRejected > it.QuantityReceived+0.0005 {
				errs.Problems = append(errs.Problems, fmt.Sprintf("%s row %d: accepted + rejected (%g) exceeds received (%g)", where, rowNo,
					it.QuantityAccepted+it.QuantityRejected, it.QuantityReceived))
			}
			g.Items = append(g.Items, it)
		}
	}
	if len(errs.Problems) > 0 {
		return out, userErr(ErrInvalid, errs.Error())
	}
	return out, nil
}

func blankRow(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// ParseCSV reads one CSV file of the given kind.
func ParseCSV(kind string, r io.Reader) (ProcurementImport, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return ProcurementImport{}, userErr(ErrInvalid, "invalid CSV: "+err.Error())
	}
	return ParseRows(kind, "CSV", rows, false)
}

// sheetKind recognises a worksheet by its name.
func sheetKind(name string) string {
	n := normHeader(name)
	switch {
	case strings.Contains(n, "vendor") || strings.Contains(n, "supplier"):
		return "vendors"
	case strings.Contains(n, "grn") || strings.Contains(n, "receipt") || strings.Contains(n, "goods"):
		return "goods_receipt_notes"
	case strings.Contains(n, "purchase") || n == "po" || n == "pos" || strings.HasPrefix(n, "po ") || strings.Contains(n, "order"):
		return "purchase_orders"
	}
	return ""
}

// ParseWorkbook reads an .xlsx workbook. Sheets named like "Vendors", "Purchase Orders", "GRNs" are
// imported together; a single unrecognised sheet is read as defaultKind.
func ParseWorkbook(r io.Reader, defaultKind string) (ProcurementImport, error) {
	var out ProcurementImport
	f, err := excelize.OpenReader(r)
	if err != nil {
		return out, userErr(ErrInvalid, "not a readable .xlsx workbook: "+err.Error())
	}
	defer f.Close()
	sheets := f.GetSheetList()
	used := 0
	var errs []string
	for _, sh := range sheets {
		kind := sheetKind(sh)
		if kind == "" && len(sheets) == 1 {
			kind = defaultKind
		}
		if kind == "" {
			continue // notes / instructions sheets
		}
		rows, err := f.GetRows(sh, excelize.Options{RawCellValue: true})
		if err != nil {
			return out, userErr(ErrInvalid, fmt.Sprintf("sheet %q: %v", sh, err))
		}
		part, err := ParseRows(kind, "sheet "+strconvQuote(sh), rows, true)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		used++
		out.Vendors = append(out.Vendors, part.Vendors...)
		out.PurchaseOrders = append(out.PurchaseOrders, part.PurchaseOrders...)
		out.GoodsReceiptNotes = append(out.GoodsReceiptNotes, part.GoodsReceiptNotes...)
	}
	if len(errs) > 0 {
		return out, userErr(ErrInvalid, strings.Join(errs, "\n"))
	}
	if used == 0 {
		return out, userErr(ErrInvalid, "no sheet named like Vendors, Purchase Orders or Goods Receipt Notes (or choose the kind for a single-sheet file)")
	}
	return out, nil
}

func strconvQuote(s string) string { return `"` + s + `"` }

var templateSample = map[string][]string{
	"vendors":             {"Acme Industrial Supplies Pvt Ltd", "27AAPFU0939F1ZV", "Plot 12, MIDC, Pune", "yes", "ACTIVE"},
	"purchase_orders":     {"PO-1001", "27AAPFU0939F1ZV", "2026-10-01", "Inno Manufacturing Ltd", "INR", "APPROVED", "1", "BRG-6205", "Deep Groove Ball Bearing 6205", "84821011", "100", "Nos", "245.50", "18"},
	"goods_receipt_notes": {"GRN-5001", "PO-1001", "27AAPFU0939F1ZV", "2026-10-10", "1", "BRG-6205", "Deep Groove Ball Bearing 6205", "100", "90", "10", "Nos"},
}

var templateSheet = map[string]string{"vendors": "Vendors", "purchase_orders": "Purchase Orders", "goods_receipt_notes": "Goods Receipt Notes"}

func templateHeader(kind string) []string {
	var h []string
	for _, c := range importColumns[kind] {
		h = append(h, strings.TrimSuffix(c, "*"))
	}
	return h
}

// TemplateCSV returns a CSV template (header + one example row) for a kind.
func TemplateCSV(kind string) ([]byte, error) {
	if _, ok := importColumns[kind]; !ok {
		return nil, userErr(ErrInvalid, "kind must be vendors, purchase_orders or goods_receipt_notes")
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.Write(templateHeader(kind))
	w.Write(templateSample[kind])
	w.Flush()
	return b.Bytes(), w.Error()
}

// TemplateXLSX returns a workbook with one sheet per kind (header + example row) and a notes sheet.
func TemplateXLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Color: []string{"#DBEAFE"}, Pattern: 1}})
	first := true
	for _, kind := range []string{"vendors", "purchase_orders", "goods_receipt_notes"} {
		name := templateSheet[kind]
		if first {
			f.SetSheetName("Sheet1", name)
			first = false
		} else {
			f.NewSheet(name)
		}
		for i, h := range templateHeader(kind) {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			f.SetCellValue(name, cell, h)
			f.SetCellStyle(name, cell, cell, bold)
			f.SetColWidth(name, colName(i+1), colName(i+1), math.Max(12, float64(len(h))+2))
		}
		for i, v := range templateSample[kind] {
			cell, _ := excelize.CoordinatesToCellName(i+1, 2)
			if n, err := strconv.ParseFloat(v, 64); err == nil && !strings.HasPrefix(v, "0") && !isCodeColumn(templateHeader(kind)[i]) {
				f.SetCellValue(name, cell, n)
			} else {
				f.SetCellValue(name, cell, v)
			}
		}
	}
	f.NewSheet("Notes")
	notes := []string{
		"One row per line item. Repeat the PO/GRN header columns on every row of the same document.",
		"Required columns: legal_name, gstin (Vendors); po_number, vendor_gstin, quantity_ordered, unit_price (Purchase Orders);",
		"grn_number, po_number, quantity_received (Goods Receipt Notes).",
		"Dates: YYYY-MM-DD, DD/MM/YYYY or Excel date cells. Numbers without currency symbols.",
		"Common alternative column names are accepted (PO No, Qty, Rate, UOM, GST %, Received Qty, ...).",
		"Delete the example rows before importing your data. Nothing is saved if any row has an error.",
	}
	for i, n := range notes {
		f.SetCellValue("Notes", fmt.Sprintf("A%d", i+1), n)
	}
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func isCodeColumn(h string) bool {
	return strings.Contains(h, "gstin") || strings.Contains(h, "hsn") || strings.Contains(h, "number") || strings.Contains(h, "code")
}

func colName(i int) string { n, _ := excelize.ColumnNumberToName(i); return n }
