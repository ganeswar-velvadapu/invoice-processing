package parse

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
)

// FromVLM converts a vision model's transcription into the same Data the rules parser produces.
// The model's answer is treated as one unverified reading: every value gets its confidence from
// our own deterministic checks, so the rest of the pipeline (review, validation, matching,
// decisions) treats AI output exactly like any other extraction.
//
//	value also present in the PDF's own text layer  -> high (0.97); absent although the page has text -> low (0.55)
//	no text layer (scan)                              -> 0.85, raised only by arithmetic / master data
//	line: qty × price − discount = amount             -> 0.95; inconsistent -> low
//	GSTIN: known vendor / own GSTIN                   -> 0.97; snapped from 1-3 character misread -> 0.8
//	totals: taxable + tax + other + round-off = total  -> 0.97 (crossCheckTotals)
func FromVLM(v *extract.VLMDoc, docType string, text []extract.Line, opt Options) *Data {
	out := &Data{DocumentType: docType, Fields: map[string]*Field{}}
	ev := newEvidence(text)

	setStr := func(name, key string, val *string, clean func(string) string) {
		if val == nil {
			return
		}
		s := strings.TrimSpace(*val)
		if clean != nil {
			s = clean(s)
		}
		if s == "" {
			return
		}
		page := v.FieldPage[key]
		out.Fields[name] = &Field{Value: s, Confidence: ev.confText(s, page), Page: page, Source: "vlm"}
	}
	setDate := func(name, key string, val *string) {
		if val == nil {
			return
		}
		t, ok := ParseDate(strings.TrimSpace(*val))
		if !ok {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s %q from the model is not a valid date", name, *val))
			return
		}
		page := v.FieldPage[key]
		out.Fields[name] = &Field{Value: t.Format("2006-01-02"), Confidence: ev.confDate(t.Format("2006-01-02"), page), Page: page, Source: "vlm"}
	}
	setNum := func(name, key string, val *float64) {
		if val == nil {
			return
		}
		page := v.FieldPage[key]
		out.Fields[name] = &Field{Value: fmtNum(round2(*val)), Confidence: ev.confNum(*val, page), Page: page, Source: "vlm"}
	}
	docNum := func(s string) string { return docNumberValue(strings.TrimLeft(s, ":#. ")) }

	// parties: resolve GSTINs against master data, then decide seller vs buyer
	sellerG, buyerG := resolveGSTIN(v.Seller.GSTIN, opt, ev, v.FieldPage["seller_gstin"]), resolveGSTIN(v.Buyer.GSTIN, opt, ev, v.FieldPage["buyer_gstin"])
	sellerName, buyerName := v.Seller.Name, v.Buyer.Name
	if swapParties(sellerG, buyerG, opt) {
		sellerG, buyerG = buyerG, sellerG
		sellerName, buyerName = buyerName, sellerName
		out.Warnings = append(out.Warnings, "seller and buyer swapped to match master data (our GSTIN is the buyer's)")
	}

	switch docType {
	case "INVOICE":
		setStr("invoice_number", "document_number", v.DocumentNumber, docNum)
		setDate("invoice_date", "document_date", v.DocumentDate)
		setDate("due_date", "due_date", v.DueDate)
		setStr("po_reference", "po_reference", v.POReference, cleanRefs)
		setStr("seller_name", "seller_name", sellerName, nil)
		setStr("buyer_name", "buyer_name", buyerName, nil)
		setStr("payment_terms", "payment_terms", v.PaymentTerms, nil)
		putField(out, "seller_gstin", sellerG)
		putField(out, "buyer_gstin", buyerG)
		setNum("subtotal", "subtotal", v.Subtotal)
		setNum("taxable_amount", "taxable_amount", v.TaxableAmount)
		setNum("cgst", "cgst", v.CGST)
		setNum("sgst", "sgst", v.SGST)
		setNum("igst", "igst", v.IGST)
		setNum("total_tax", "total_tax", v.TotalTax)
		setNum("other_charges", "other_charges", v.OtherCharges)
		setNum("round_off", "round_off", v.RoundOff)
		setNum("grand_total", "grand_total", v.GrandTotal)
	case "PO":
		setStr("po_number", "document_number", v.DocumentNumber, docNum)
		setDate("po_date", "document_date", v.DocumentDate)
		setStr("vendor_name", "seller_name", sellerName, nil)
		setStr("buyer_name", "buyer_name", buyerName, nil)
		setStr("payment_terms", "payment_terms", v.PaymentTerms, nil)
		putField(out, "vendor_gstin", sellerG)
		putField(out, "buyer_gstin", buyerG)
		if v.GrandTotal != nil {
			setNum("total_amount", "grand_total", v.GrandTotal)
		} else {
			setNum("total_amount", "subtotal", v.Subtotal)
		}
	case "GRN":
		setStr("grn_number", "document_number", v.DocumentNumber, docNum)
		setDate("receipt_date", "document_date", v.DocumentDate)
		setStr("po_reference", "po_reference", v.POReference, cleanRefs)
		setStr("vendor_name", "seller_name", sellerName, nil)
		putField(out, "vendor_gstin", sellerG)
		setStr("invoice_reference", "invoice_reference", v.InvoiceReference, docNum)
	default:
		setStr("document_number", "document_number", v.DocumentNumber, docNum)
		setDate("document_date", "document_date", v.DocumentDate)
	}
	if docType == "INVOICE" || docType == "PO" {
		cur := "INR"
		if v.Currency != nil && strings.TrimSpace(*v.Currency) != "" {
			cur = normCurrency(*v.Currency)
		}
		out.Fields["currency"] = &Field{Value: cur, Confidence: 0.9, Source: "vlm"}
	}

	for i, mi := range v.Items {
		it := Item{LineNumber: i + 1, Source: "vlm", Page: mi.Page,
			ItemCode: s(mi.ItemCode), Description: strings.Join(strings.Fields(s(mi.Description)), " "), HSNSAC: digitsOnly(s(mi.HSN)),
			Unit: s(mi.Unit), UnitPrice: mi.UnitPrice, Discount: mi.Discount, TaxableValue: mi.TaxableValue,
			TaxRate: mi.TaxRate, TaxAmount: mi.TaxAmount, LineTotal: mi.Amount}
		switch docType {
		case "GRN":
			it.Quantity, it.QuantityReceived, it.QuantityAccepted, it.QuantityRejected = mi.Quantity, mi.QtyReceived, mi.QtyAccepted, mi.QtyRejected
			if it.QuantityReceived == nil {
				it.QuantityReceived = mi.Quantity // a receipt without separate columns lists what arrived
			}
		default:
			it.Quantity = mi.Quantity
		}
		if it.Description == "" && it.ItemCode == "" && it.Quantity == nil && it.QuantityReceived == nil {
			continue // empty row
		}
		finishItem(&it, docType, "") // derive missing values + arithmetic confidence (0.92 consistent / lower if not)
		it.Source = "vlm"
		// Values the model read must also be printed in the PDF's text layer (when the page has one).
		if ev.pageHasText(it.Page) {
			for _, n := range []*float64{it.Quantity, it.UnitPrice, it.QuantityReceived, it.QuantityAccepted} {
				if n != nil && !ev.hasNum(*n) {
					it.Confidence = math.Min(it.Confidence, 0.55)
				}
			}
		}
		it.Confidence = round4(it.Confidence)
		it.LineNumber = len(out.Items) + 1
		out.Items = append(out.Items, it)
	}
	if docType == "INVOICE" {
		crossCheckTotals(out)
	}
	out.Warnings = append(out.Warnings, fmt.Sprintf("extracted by vision model %s (%d page(s), %.0fs)", v.ModelName, v.Pages, v.Duration.Seconds()))
	return out
}

func s(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normCurrency(c string) string {
	u := strings.ToUpper(strings.TrimSpace(c))
	switch {
	case strings.Contains(u, "INR"), strings.Contains(u, "₹"), strings.HasPrefix(u, "RS"), strings.Contains(u, "RUPEE"):
		return "INR"
	case strings.Contains(u, "USD"), u == "$":
		return "USD"
	case strings.Contains(u, "EUR"), u == "€":
		return "EUR"
	}
	return u
}

var refSplitRe = regexp.MustCompile(`\s*(,|&|;|\band\b|\s/\s)\s*`)

// cleanRefs keeps the document-number-shaped parts of a PO reference ("PO-1, PO-2").
func cleanRefs(s string) string {
	var refs []string
	for _, p := range refSplitRe.Split(s, -1) {
		if v := docNumberValue(strings.TrimLeft(p, ":#. ")); v != "" {
			refs = append(refs, v)
		}
	}
	return strings.Join(dedupe(refs), ",")
}

// resolveGSTIN validates a GSTIN read by the model and repairs it against master data.
func resolveGSTIN(raw *string, opt Options, ev *evidence, page int) *Field {
	if raw == nil {
		return nil
	}
	g := gstin.Normalize(*raw)
	if len(g) < 13 {
		return nil
	}
	known := append(append([]string{}, opt.VendorGSTINs...), opt.BuyerGSTINs...)
	isKnown := func(x string) bool {
		for _, k := range known {
			if gstin.Normalize(k) == x {
				return true
			}
		}
		return false
	}
	f := &Field{Value: g, Page: page, Source: "vlm"}
	switch r, _ := gstin.Check(g); {
	case isKnown(g):
		f.Confidence = 0.97
	case len(known) > 0:
		if k, ok := SnapGSTIN(g, known); ok {
			f.Value, f.Confidence, f.Source = k, 0.8, "vlm+master_snap"
		} else if r.Valid {
			f.Confidence = ev.confText(g, page)
		} else {
			f.Confidence = 0.4
		}
	case r.Valid:
		f.Confidence = ev.confText(g, page)
	default:
		if rep, ok := gstin.Repair(g); ok {
			f.Value, f.Confidence = rep, 0.8
		} else {
			f.Confidence = 0.4
		}
	}
	return f
}

// swapParties: the model mixed up seller and buyer if our own GSTIN is on the seller side
// or a known vendor is on the buyer side.
func swapParties(seller, buyer *Field, opt Options) bool {
	in := func(f *Field, list []string) bool {
		if f == nil {
			return false
		}
		for _, g := range list {
			if gstin.Normalize(g) == f.Value {
				return true
			}
		}
		return false
	}
	return (in(seller, opt.BuyerGSTINs) && !in(buyer, opt.BuyerGSTINs)) || (in(buyer, opt.VendorGSTINs) && !in(seller, opt.VendorGSTINs))
}

func putField(out *Data, name string, f *Field) {
	if f != nil && f.Value != "" {
		out.Fields[name] = f
	}
}

// evidence indexes the PDF's own text layer so the model's values can be verified against it.
type evidence struct {
	pages   map[int]bool
	compact string           // upper-case alphanumerics of the whole text layer
	nums    map[float64]bool // every number printed (2 decimals)
	dates   map[string]bool  // every date printed, as YYYY-MM-DD
}

func newEvidence(lines []extract.Line) *evidence {
	ev := &evidence{pages: map[int]bool{}, nums: map[float64]bool{}, dates: map[string]bool{}}
	var b strings.Builder
	for _, l := range lines {
		ev.pages[l.Page] = true
		b.WriteString(compact(l.Text))
		b.WriteByte('|')
		for _, m := range numberRe.FindAllString(l.Text, -1) {
			if f, ok := ParseNumber(m); ok {
				ev.nums[round2(math.Abs(f))] = true
			}
		}
		for _, m := range DateRe.FindAllString(l.Text, -1) {
			if t, ok := ParseDate(m); ok {
				ev.dates[t.Format("2006-01-02")] = true
			}
		}
	}
	ev.compact = b.String()
	return ev
}

func compact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (e *evidence) pageHasText(page int) bool {
	if page == 0 {
		return len(e.pages) > 0
	}
	return e.pages[page]
}

func (e *evidence) hasNum(f float64) bool { return e.nums[round2(math.Abs(f))] }

// Without a text layer the model's value is one unverified reading (0.85).
func (e *evidence) conf(found bool, page int) float64 {
	switch {
	case !e.pageHasText(page):
		return 0.85
	case found:
		return 0.97
	default:
		return 0.55 // the model reports something the document's own text does not contain
	}
}

func (e *evidence) confText(v string, page int) float64 {
	c := compact(v)
	return e.conf(c != "" && strings.Contains(e.compact, c), page)
}
func (e *evidence) confNum(f float64, page int) float64 { return e.conf(e.hasNum(f), page) }
func (e *evidence) confDate(d string, page int) float64 { return e.conf(e.dates[d], page) }
