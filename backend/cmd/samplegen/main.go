// Command samplegen writes a handful of hand-specified sample documents (PDF) plus a
// procurement import JSON for development testing. It is intentionally small; the full
// synthetic evaluation dataset generator is a separate, later phase.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/go-pdf/fpdf"

	"invoiceproc/internal/gstin"
)

var (
	buyerGSTIN   = gstin.WithChecksum("29AAACI1234F1Z")
	acmeGSTIN    = gstin.WithChecksum("27AAPFU0939F1Z")
	bharatGSTIN  = gstin.WithChecksum("29AABCB5521K1Z")
	unknownGSTIN = gstin.WithChecksum("24AAKCS7788M1Z")
)

type line struct {
	Code, Desc, HSN, Unit string
	Qty, Rate, GST        float64
}

func r2(f float64) float64 { return math.Round(f*100) / 100 }

func money(f float64) string {
	// Indian grouping 12,34,567.89
	neg := f < 0
	f = math.Abs(f)
	s := fmt.Sprintf("%.2f", f)
	intPart, dec := s[:len(s)-3], s[len(s)-3:]
	out := ""
	if len(intPart) > 3 {
		out = "," + intPart[len(intPart)-3:]
		intPart = intPart[:len(intPart)-3]
		for len(intPart) > 2 {
			out = "," + intPart[len(intPart)-2:] + out
			intPart = intPart[:len(intPart)-2]
		}
		out = intPart + out
	} else {
		out = intPart
	}
	if neg {
		out = "-" + out
	}
	return out + dec
}

func newPDF() *fpdf.Fpdf {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(12, 12, 12)
	p.AddPage()
	p.SetFont("Helvetica", "", 9)
	return p
}

func text(p *fpdf.Fpdf, x, y float64, s string) { p.Text(x, y, s) }

func bold(p *fpdf.Fpdf, size float64) { p.SetFont("Helvetica", "B", size) }
func normal(p *fpdf.Fpdf)             { p.SetFont("Helvetica", "", 9) }

// Style A: letterhead, "Invoice No / Invoice Date" labels, 11-column table, IGST totals.
func invoiceStyleA(path, invNo, date, po string, items []line, interState bool) {
	p := newPDF()
	bold(p, 14)
	text(p, 85, 15, "TAX INVOICE")
	bold(p, 11)
	text(p, 12, 25, "Acme Industrial Supplies Pvt Ltd")
	normal(p)
	text(p, 12, 30, "Plot 12, MIDC Bhosari, Pune, Maharashtra 411026")
	text(p, 12, 35, "GSTIN: "+acmeGSTIN)
	text(p, 120, 25, "Invoice No: "+invNo)
	text(p, 120, 30, "Invoice Date: "+date)
	text(p, 120, 35, "PO No: "+po)
	text(p, 120, 40, "Due Date: 14/12/2026")
	bold(p, 9)
	text(p, 12, 48, "Bill To:")
	normal(p)
	text(p, 12, 53, "Inno Manufacturing Ltd")
	text(p, 12, 58, "Peenya Industrial Area, Bengaluru, Karnataka")
	text(p, 12, 63, "GSTIN: "+buyerGSTIN)
	text(p, 120, 48, "Payment Terms: Net 30")

	xs := []float64{12, 20, 42, 92, 107, 118, 128, 145, 165, 177, 195}
	hdr := []string{"S.No", "Item Code", "Description", "HSN", "Qty", "Unit", "Rate", "Taxable Value", "GST %", "Tax Amt", "Total"}
	y := 75.0
	bold(p, 8)
	for i, h := range hdr {
		text(p, xs[i], y, h)
	}
	normal(p)
	p.SetFont("Helvetica", "", 8)
	var taxable, tax float64
	for i, it := range items {
		y += 6
		tv := r2(it.Qty * it.Rate)
		ta := r2(tv * it.GST / 100)
		taxable += tv
		tax += ta
		row := []string{fmt.Sprint(i + 1), it.Code, it.Desc, it.HSN, fmt.Sprint(it.Qty), it.Unit, money(it.Rate), money(tv), fmt.Sprintf("%g%%", it.GST), money(ta), money(tv + ta)}
		for k, c := range row {
			text(p, xs[k], y, c)
		}
	}
	normal(p)
	y += 12
	text(p, 130, y, "Taxable Value")
	text(p, 175, y, money(taxable))
	if interState {
		y += 5
		text(p, 130, y, "IGST")
		text(p, 175, y, money(tax))
	} else {
		y += 5
		text(p, 130, y, "CGST")
		text(p, 175, y, money(r2(tax/2)))
		y += 5
		text(p, 130, y, "SGST")
		text(p, 175, y, money(r2(tax/2)))
	}
	gt := taxable + tax
	ro := r2(math.Round(gt) - gt)
	y += 5
	text(p, 130, y, "Round Off")
	text(p, 175, y, money(ro))
	y += 6
	bold(p, 10)
	text(p, 130, y, "Grand Total")
	text(p, 175, y, money(gt+ro))
	normal(p)
	text(p, 12, y+12, "Amount in words: Rupees as above only")
	text(p, 140, y+30, "For Acme Industrial Supplies Pvt Ltd")
	text(p, 150, y+40, "Authorised Signatory")
	must(p.OutputFileAndClose(path))
}

// Style B: Tally-like labels ("Bill No.", "Dated", "Buyer's Order No."), 7-column table, CGST/SGST.
func invoiceStyleB(path, invNo, date, po string, items []line) {
	p := newPDF()
	bold(p, 13)
	text(p, 12, 15, "Bharat Electricals & Controls")
	normal(p)
	text(p, 12, 20, "No. 45, 2nd Cross, Rajajinagar, Bengaluru 560010")
	text(p, 12, 25, "GSTIN/UIN: "+bharatGSTIN)
	bold(p, 12)
	text(p, 160, 15, "INVOICE")
	normal(p)
	text(p, 120, 32, "Bill No.")
	text(p, 160, 32, invNo)
	text(p, 120, 37, "Dated")
	text(p, 160, 37, date)
	text(p, 120, 42, "Buyer's Order No.")
	text(p, 160, 42, po)
	text(p, 12, 35, "Buyer (Bill to)")
	text(p, 12, 40, "Inno Manufacturing Ltd")
	text(p, 12, 45, "GSTIN/UIN: "+buyerGSTIN)

	xs := []float64{12, 22, 95, 115, 135, 155, 172}
	hdr := []string{"Sl", "Description of Goods", "HSN/SAC", "Quantity", "Rate", "per", "Amount"}
	y := 58.0
	bold(p, 9)
	for i, h := range hdr {
		text(p, xs[i], y, h)
	}
	normal(p)
	var taxable float64
	gstRate := 18.0
	for i, it := range items {
		y += 6
		amt := r2(it.Qty * it.Rate)
		taxable += amt
		row := []string{fmt.Sprint(i + 1), it.Code + " " + it.Desc, it.HSN, fmt.Sprintf("%g %s", it.Qty, it.Unit), money(it.Rate), it.Unit, money(amt)}
		for k, c := range row {
			text(p, xs[k], y, c)
		}
		gstRate = it.GST
	}
	half := r2(taxable * gstRate / 200)
	y += 10
	text(p, 115, y, fmt.Sprintf("CGST @ %g%%", gstRate/2))
	text(p, 172, y, money(half))
	y += 5
	text(p, 115, y, fmt.Sprintf("SGST @ %g%%", gstRate/2))
	text(p, 172, y, money(half))
	y += 7
	bold(p, 10)
	text(p, 115, y, "Total")
	text(p, 172, y, money(taxable+2*half))
	normal(p)
	text(p, 12, y+10, "Amount Chargeable (in words): INR as above Only")
	must(p.OutputFileAndClose(path))
}

func purchaseOrder(path, poNo, date, vendorName, vendorGSTIN string, items []line) {
	p := newPDF()
	bold(p, 13)
	text(p, 12, 15, "Inno Manufacturing Ltd")
	normal(p)
	text(p, 12, 20, "Peenya Industrial Area, Bengaluru, Karnataka")
	text(p, 12, 25, "GSTIN: "+buyerGSTIN)
	bold(p, 14)
	text(p, 80, 35, "PURCHASE ORDER")
	normal(p)
	text(p, 130, 45, "PO Number: "+poNo)
	text(p, 130, 50, "PO Date: "+date)
	text(p, 12, 45, "Vendor:")
	text(p, 12, 50, vendorName)
	text(p, 12, 55, "GSTIN: "+vendorGSTIN)
	xs := []float64{12, 22, 45, 105, 125, 145, 170}
	hdr := []string{"Line", "Item Code", "Description", "HSN", "Qty Ordered", "Unit Price", "Amount"}
	y := 68.0
	bold(p, 9)
	for i, h := range hdr {
		text(p, xs[i], y, h)
	}
	normal(p)
	var total float64
	for i, it := range items {
		y += 6
		amt := r2(it.Qty * it.Rate)
		total += amt
		row := []string{fmt.Sprint(i + 1), it.Code, it.Desc, it.HSN, fmt.Sprintf("%g", it.Qty), money(it.Rate), money(amt)}
		for k, c := range row {
			text(p, xs[k], y, c)
		}
	}
	y += 10
	text(p, 125, y, "Total Amount")
	text(p, 170, y, money(total))
	must(p.OutputFileAndClose(path))
}

func grn(path, grnNo, date, poNo string, items []line, accepted []float64) {
	p := newPDF()
	bold(p, 14)
	text(p, 60, 15, "GOODS RECEIPT NOTE")
	normal(p)
	text(p, 12, 25, "Inno Manufacturing Ltd - Stores, Bengaluru")
	text(p, 12, 32, "GRN No: "+grnNo)
	text(p, 12, 37, "Receipt Date: "+date)
	text(p, 120, 32, "PO No: "+poNo)
	text(p, 120, 37, "Supplier: Acme Industrial Supplies Pvt Ltd")
	text(p, 120, 42, "Supplier GSTIN: "+acmeGSTIN)
	xs := []float64{12, 22, 45, 110, 135, 160, 185}
	hdr := []string{"Line", "Item Code", "Description", "Qty Received", "Qty Accepted", "Qty Rejected", "UOM"}
	y := 55.0
	bold(p, 9)
	for i, h := range hdr {
		text(p, xs[i], y, h)
	}
	normal(p)
	for i, it := range items {
		y += 6
		row := []string{fmt.Sprint(i + 1), it.Code, it.Desc, fmt.Sprintf("%g", it.Qty), fmt.Sprintf("%g", accepted[i]), fmt.Sprintf("%g", it.Qty-accepted[i]), it.Unit}
		for k, c := range row {
			text(p, xs[k], y, c)
		}
	}
	text(p, 12, y+15, "Received by: Stores In-charge")
	must(p.OutputFileAndClose(path))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	out := flag.String("out", "testdata", "output directory (relative to backend/)")
	flag.Parse()
	must(os.MkdirAll(*out, 0o755))
	acmeItems := []line{
		{"BRG-6205", "Deep Groove Ball Bearing 6205", "84821011", "Nos", 100, 245.50, 18},
		{"VBL-B42", "V-Belt Classical B42", "40103190", "Nos", 50, 312.00, 18},
		{"GRS-LT2", "Lithium Grease LT-2 (1kg)", "34039900", "Kg", 20, 410.00, 18},
	}
	bharatItems := []line{
		{"MCB-32A", "MCB Double Pole 32A", "85362030", "Nos", 40, 525.00, 18},
		{"CBL-4SQ", "Copper Cable 4 sq mm (90m)", "85444999", "Roll", 10, 6150.00, 18},
	}
	// PO-1001: Acme; GRN accepted 90 of 100 bearings -> invoice for 100 must be held.
	purchaseOrder(filepath.Join(*out, "po_PO-1001_acme.pdf"), "PO-1001", "01/10/2026", "Acme Industrial Supplies Pvt Ltd", acmeGSTIN, acmeItems)
	grn(filepath.Join(*out, "grn_GRN-5001_acme.pdf"), "GRN-5001", "10/10/2026", "PO-1001", acmeItems, []float64{90, 50, 20})
	invoiceStyleA(filepath.Join(*out, "inv_INV-2026-0042_acme_qty_exceeds_grn.pdf"), "INV-2026-0042", "15/10/2026", "PO-1001", acmeItems, true)
	// clean match: invoice only what was accepted
	clean := append([]line{}, acmeItems...)
	clean[0].Qty = 90
	invoiceStyleA(filepath.Join(*out, "inv_INV-2026-0043_acme_clean.pdf"), "INV-2026-0043", "16/10/2026", "PO-1001", clean[:1], true)
	// Bharat: second layout, PO + GRN come via JSON import; price above PO on line 2
	priced := append([]line{}, bharatItems...)
	priced[1].Rate = 6400.00
	invoiceStyleB(filepath.Join(*out, "inv_BEC-778_bharat_price_mismatch.pdf"), "BEC/26-27/778", "20-Oct-2026", "PO-1002", priced)
	invoiceStyleB(filepath.Join(*out, "inv_BEC-779_bharat_clean.pdf"), "BEC/26-27/779", "21-Oct-2026", "PO-1002", bharatItems[:1])

	imp := map[string]any{
		"vendors": []map[string]any{
			{"legal_name": "Acme Industrial Supplies Pvt Ltd", "gstin": acmeGSTIN, "address": "Pune, Maharashtra", "is_approved": true},
			{"legal_name": "Bharat Electricals & Controls", "gstin": bharatGSTIN, "address": "Bengaluru, Karnataka", "is_approved": true},
			{"legal_name": "Shree Packaging Works", "gstin": unknownGSTIN, "address": "Ahmedabad", "is_approved": false},
		},
		"purchase_orders": []map[string]any{{
			"po_number": "PO-1002", "vendor_gstin": bharatGSTIN, "po_date": "2026-10-05", "buyer_entity": "Inno Manufacturing Ltd", "currency": "INR",
			"items": []map[string]any{
				{"line_number": 1, "item_code": "MCB-32A", "description": "MCB Double Pole 32A", "hsn_sac": "85362030", "quantity_ordered": 40, "unit_of_measure": "Nos", "unit_price": 525.00, "tax_rate": 18},
				{"line_number": 2, "item_code": "CBL-4SQ", "description": "Copper Cable 4 sq mm (90m)", "hsn_sac": "85444999", "quantity_ordered": 10, "unit_of_measure": "Roll", "unit_price": 6150.00, "tax_rate": 18},
			},
		}},
		"goods_receipt_notes": []map[string]any{{
			"grn_number": "GRN-5002", "po_number": "PO-1002", "vendor_gstin": bharatGSTIN, "receipt_date": "2026-10-18",
			"items": []map[string]any{
				{"line_number": 1, "item_code": "MCB-32A", "description": "MCB Double Pole 32A", "quantity_received": 40, "quantity_accepted": 40, "quantity_rejected": 0, "unit_of_measure": "Nos"},
				{"line_number": 2, "item_code": "CBL-4SQ", "description": "Copper Cable 4 sq mm (90m)", "quantity_received": 10, "quantity_accepted": 10, "quantity_rejected": 0, "unit_of_measure": "Roll"},
			},
		}},
	}
	b, _ := json.MarshalIndent(imp, "", "  ")
	must(os.WriteFile(filepath.Join(*out, "procurement_import.json"), b, 0o644))
	fmt.Println("buyer GSTIN:", buyerGSTIN, "acme:", acmeGSTIN, "bharat:", bharatGSTIN)
}
