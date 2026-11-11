package parse

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"invoiceproc/internal/extract"
)

// ReconcileProblems decides whether a rules-based reading of a digital PDF can be trusted.
// The document's own printed numbers are the answer key: a misparse (missed lines, a wrong total
// picked, a document number cut mid-word) almost never still reconciles. Any problem returned
// sends the document to the vision model instead.
func ReconcileProblems(d *Data, lines []extract.Line) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	num := func(k string) (float64, bool) { return fval(d, k) }
	// tolerance for sums: rounding of each printed line (1 paisa per line), at least 5 paise
	tol := func(n int) float64 { return math.Max(0.05, 0.01*float64(n)) }

	if len(d.Items) == 0 {
		add("no line items were read")
	}
	for _, it := range d.Items {
		if it.Confidence < 0.85 {
			add("line %d does not compute (quantity × price ≠ amount)", it.LineNumber)
		}
	}
	text := wholeText(lines)
	for _, k := range []string{"invoice_number", "po_number", "grn_number"} {
		if f := d.Fields[k]; f != nil && f.Value != "" && !wholeToken(text, f.Value) {
			add("%s %q is not a complete word in the document text", k, f.Value)
		}
	}

	switch d.DocumentType {
	case "INVOICE":
		var sumTaxable, sumTax float64
		allTax := true
		for _, it := range d.Items {
			if it.TaxableValue != nil {
				sumTaxable += *it.TaxableValue
			}
			if it.TaxAmount != nil {
				sumTax += *it.TaxAmount
			} else {
				allTax = false
			}
		}
		taxable, okT := num("taxable_amount")
		if !okT {
			taxable, okT = num("subtotal")
		}
		if okT && len(d.Items) > 0 && math.Abs(sumTaxable-taxable) > tol(len(d.Items)) {
			add("lines add up to %.2f but the document says %.2f (lines missing or misread)", sumTaxable, taxable)
		}
		cg, okC := num("cgst")
		sg, okS := num("sgst")
		ig, okI := num("igst")
		total, okX := num("total_tax")
		if okX && (okC || okS || okI) && math.Abs(cg+sg+ig-total) > 0.05 {
			add("CGST + SGST + IGST = %.2f but total tax is %.2f", cg+sg+ig, total)
		}
		if okX && allTax && len(d.Items) > 0 && math.Abs(sumTax-total) > tol(len(d.Items)) {
			add("line taxes add up to %.2f but total tax is %.2f", sumTax, total)
		}
		gt, okG := num("grand_total")
		other, _ := num("other_charges")
		ro, _ := num("round_off")
		if !okG {
			add("grand total not found")
		} else if okT && okX && math.Abs(taxable+total+other+ro-gt) > 0.05 {
			add("taxable %.2f + tax %.2f + other %.2f + round-off %.2f ≠ grand total %.2f", taxable, total, other, ro, gt)
		}
	case "PO":
		if t, ok := num("total_amount"); ok && len(d.Items) > 0 {
			var s float64
			for _, it := range d.Items {
				if it.Quantity != nil && it.UnitPrice != nil {
					s += *it.Quantity * *it.UnitPrice
				}
			}
			// a PO total may or may not include GST: accept either
			if math.Abs(s-t) > tol(len(d.Items)) && math.Abs(s*1.18-t) > tol(len(d.Items)) && t > 0 {
				var withTax float64
				for _, it := range d.Items {
					if it.Quantity != nil && it.UnitPrice != nil && it.TaxRate != nil {
						withTax += *it.Quantity * *it.UnitPrice * (1 + *it.TaxRate/100)
					}
				}
				if math.Abs(withTax-t) > tol(len(d.Items)) {
					add("PO lines add up to %.2f but the document total is %.2f", s, t)
				}
			}
		}
	case "GRN":
		for _, it := range d.Items {
			if it.QuantityReceived == nil || it.QuantityAccepted == nil {
				add("line %d has no received/accepted quantity", it.LineNumber)
			}
		}
	}
	return p
}

func wholeText(lines []extract.Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// wholeToken: the value occurs in the text with no letter/digit/joiner directly before or after it,
// so "4-00000" does not count as present in "GST-2024-00000".
func wholeToken(text, v string) bool {
	re, err := regexp.Compile(`(?i)(^|[^A-Za-z0-9/\-_.])` + regexp.QuoteMeta(v) + `($|[^A-Za-z0-9/\-_])`)
	if err != nil {
		return true
	}
	return re.MatchString(text)
}
