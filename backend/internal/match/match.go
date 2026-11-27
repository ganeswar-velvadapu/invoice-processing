// Package match identifies the purchase order(s) behind an invoice, allocates invoice lines to PO
// lines and performs two-/three-way checks against ordered, previously invoiced and accepted quantities.
package match

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"invoiceproc/internal/model"
	"invoiceproc/internal/utils"
)

const (
	MethodExact  = "EXACT_REFERENCE"
	MethodRule   = "RULE_BASED"
	MethodManual = "MANUAL"

	StatusCandidate = "CANDIDATE"
	StatusConfirmed = "CONFIRMED"
	StatusRejected  = "REJECTED"
)

type Input struct {
	Invoice       *model.Invoice
	VendorID      string
	ReferencedPOs []model.PurchaseOrder // POs whose number matches an invoice PO reference
	MissingRefs   []string              // references with no PO on file
	ApproxRefs    map[string]string     // PO id -> reference as printed, matched with an OCR-slip tolerance
	VendorPOs     []model.PurchaseOrder // open POs of the vendor, for candidate search
	ManualPOs     []model.PurchaseOrder // POs a user confirmed manually (take precedence)
	RejectedPOIDs map[string]bool       // POs a user rejected for this invoice
	Tolerance     model.Tolerance
}

type POMatch struct {
	PurchaseOrderID string   `json:"purchase_order_id"`
	PONumber        string   `json:"po_number"`
	Method          string   `json:"match_method"`
	Status          string   `json:"match_status"`
	Confidence      float64  `json:"match_confidence"`
	Evidence        []string `json:"evidence"`
}

type Allocation struct {
	InvoiceItemID string  `json:"invoice_item_id"`
	InvoiceLine   int     `json:"invoice_line"`
	POItemID      string  `json:"purchase_order_item_id"`
	PONumber      string  `json:"po_number"`
	POLine        int     `json:"po_line"`
	Quantity      float64 `json:"allocated_quantity"`
	Amount        float64 `json:"allocated_amount"`
	Basis         string  `json:"match_basis"`
}

type Result struct {
	POMatches   []POMatch
	Allocations []Allocation
	Checks      []model.Check
	GRNIDs      []string
}

func money(f float64) string { return fmt.Sprintf("%.2f", f) }
func qty(f float64) string   { return utils.FormatQty(f) }

func vendorMatches(po model.PurchaseOrder, inv *model.Invoice, vendorID string) bool {
	if vendorID != "" && po.VendorID != "" {
		return po.VendorID == vendorID
	}
	return po.VendorGSTIN != "" && po.VendorGSTIN == inv.SellerGSTIN
}

// lineScore rates how well an invoice line corresponds to a PO line (0..1) and explains why.
func lineScore(it model.InvoiceItem, pi model.POItem) (float64, string) {
	if it.ItemCode != "" && pi.ItemCode != "" && utils.NormalizeRef(it.ItemCode) == utils.NormalizeRef(pi.ItemCode) {
		return 1.0, "item code " + pi.ItemCode
	}
	s := 0.0
	var why []string
	sim := utils.TextSimilarity(it.Description, pi.Description)
	if pi.ItemCode != "" && strings.Contains(utils.NormalizeRef(it.Description), utils.NormalizeRef(pi.ItemCode)) {
		sim = math.Max(sim, 0.9)
	}
	s += 0.6 * sim
	if sim > 0 {
		why = append(why, fmt.Sprintf("description similarity %.0f%%", sim*100))
	}
	if it.HSNSAC != "" && pi.HSNSAC != "" && strings.HasPrefix(it.HSNSAC, pi.HSNSAC[:min(4, len(pi.HSNSAC))]) {
		s += 0.2
		why = append(why, "HSN "+pi.HSNSAC)
	}
	if it.UnitPrice != nil && pi.UnitPrice > 0 && math.Abs(*it.UnitPrice-pi.UnitPrice)/pi.UnitPrice <= 0.05 {
		s += 0.2
		why = append(why, "unit price within 5%")
	}
	return math.Min(s, 0.99), strings.Join(why, ", ")
}

// Run performs PO identification, line allocation and quantity/price/tax/GRN checks.
func Run(in Input) Result {
	inv := in.Invoice
	var r Result
	tol := in.Tolerance

	// ---------------------------------------------------------------- 1. PO identification
	var matched []model.PurchaseOrder
	switch {
	case len(in.ManualPOs) > 0:
		for _, po := range in.ManualPOs {
			matched = append(matched, po)
			r.POMatches = append(r.POMatches, POMatch{po.PurchaseOrderID, po.PONumber, MethodManual, StatusConfirmed, 1,
				[]string{"confirmed manually by AP"}})
		}
		r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Pass, Actual: poNumbers(matched),
			Reason: "Purchase order(s) " + poNumbers(matched) + " were confirmed manually by AP."})
	case len(in.ReferencedPOs) > 0:
		var wrongVendor, inactive []string
		for _, po := range in.ReferencedPOs {
			if in.RejectedPOIDs[po.PurchaseOrderID] {
				continue
			}
			ev := []string{"invoice references " + po.PONumber}
			method := MethodExact
			if printed, ok := in.ApproxRefs[po.PurchaseOrderID]; ok {
				ev = []string{"invoice reference " + printed + " ≈ " + po.PONumber + " (one character differs, likely OCR)"}
				method = MethodRule
			}
			if !vendorMatches(po, inv, in.VendorID) {
				wrongVendor = append(wrongVendor, po.PONumber)
				r.POMatches = append(r.POMatches, POMatch{po.PurchaseOrderID, po.PONumber, method, StatusCandidate, 0.3,
					append(ev, "PO is issued to a different vendor")})
				continue
			}
			ev = append(ev, "vendor identity matches")
			if po.Status == "CANCELLED" || po.Status == "CLOSED" || po.Status == "DRAFT" {
				inactive = append(inactive, po.PONumber+" ("+po.Status+")")
			}
			matched = append(matched, po)
			conf := 1.0
			if method == MethodRule {
				conf = 0.9
			}
			r.POMatches = append(r.POMatches, POMatch{po.PurchaseOrderID, po.PONumber, method, StatusConfirmed, conf, ev})
		}
		switch {
		case len(wrongVendor) > 0:
			r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "PO_VENDOR_MISMATCH",
				Actual: strings.Join(wrongVendor, ", "), Reason: "Referenced PO " + strings.Join(wrongVendor, ", ") + " belongs to a different vendor than the invoice supplier."})
		case len(inactive) > 0:
			r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "PO_NOT_OPEN",
				Actual: strings.Join(inactive, ", "), Reason: "Referenced PO is not open for invoicing: " + strings.Join(inactive, ", ") + "."})
		case len(matched) > 0:
			reason := "Invoice references " + poNumbers(matched) + " and the vendor identity matches."
			if len(in.MissingRefs) > 0 {
				reason += " Reference(s) " + strings.Join(in.MissingRefs, ", ") + " were not found and were ignored."
			}
			r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Pass, Actual: poNumbers(matched), Reason: reason})
		}
	}
	if len(matched) == 0 && !hasCheck(r.Checks, "PO_MATCH") {
		matched = candidateSearch(in, &r)
	}
	if len(matched) == 0 {
		return r
	}

	// ---------------------------------------------------------------- 2. line allocation
	type poiRef struct {
		po   *model.PurchaseOrder
		item *model.POItem
	}
	var pool []poiRef
	for i := range matched {
		for j := range matched[i].Items {
			pool = append(pool, poiRef{&matched[i], &matched[i].Items[j]})
		}
		r.GRNIDs = append(r.GRNIDs, matched[i].GRNIDs...)
	}
	thisQty := map[string]float64{} // PO item -> qty allocated by this invoice
	var unmatched []string
	lineOf := map[string][]int{} // PO item -> invoice lines
	for _, it := range inv.Items {
		if it.Quantity == nil {
			continue
		}
		type scored struct {
			ref   poiRef
			score float64
			why   string
		}
		var cands []scored
		for _, p := range pool {
			s, why := lineScore(it, *p.item)
			if s >= 0.5 {
				cands = append(cands, scored{p, s, why})
			}
		}
		sort.SliceStable(cands, func(a, b int) bool {
			if cands[a].score != cands[b].score {
				return cands[a].score > cands[b].score
			}
			// equal score: consume POs in number order, then line order (FIFO)
			if cands[a].ref.po.PONumber != cands[b].ref.po.PONumber {
				return cands[a].ref.po.PONumber < cands[b].ref.po.PONumber
			}
			return cands[a].ref.item.LineNumber < cands[b].ref.item.LineNumber
		})
		if len(cands) == 0 {
			unmatched = append(unmatched, fmt.Sprintf("line %d (%s)", it.LineNumber, firstNonEmpty(it.ItemCode, it.Description)))
			continue
		}
		// allocate across equally good PO lines (same item on several POs / schedule lines)
		left := *it.Quantity
		price := utils.F(it.UnitPrice)
		for k, c := range cands {
			if left <= 0 {
				break
			}
			if c.score < cands[0].score-0.05 {
				break
			}
			take := left
			isLast := k == len(cands)-1 || cands[k+1].score < cands[0].score-0.05
			if rem := remaining(*c.ref.item, thisQty); !isLast && rem < left {
				take = math.Max(rem, 0)
			}
			if take <= 0 {
				continue
			}
			left -= take
			thisQty[c.ref.item.POItemID] += take
			lineOf[c.ref.item.POItemID] = append(lineOf[c.ref.item.POItemID], it.LineNumber)
			r.Allocations = append(r.Allocations, Allocation{it.InvoiceItemID, it.LineNumber, c.ref.item.POItemID, c.ref.po.PONumber,
				c.ref.item.LineNumber, utils.Round3(take), utils.Round2(take * price), c.why})
		}
	}
	if len(unmatched) > 0 {
		r.Checks = append(r.Checks, model.Check{Code: "PO_LINE_MATCH", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "ITEM_NOT_ON_PO",
			Actual: strings.Join(unmatched, "; "), Reason: "Invoice line(s) could not be matched to any line of " + poNumbers(matched) + ": " + strings.Join(unmatched, "; ") + "."})
	} else {
		r.Checks = append(r.Checks, model.Check{Code: "PO_LINE_MATCH", Status: model.Pass, Reason: "Every invoice line was matched to a PO line."})
	}

	// ---------------------------------------------------------------- 3. per-line checks
	byItem := map[string]model.InvoiceItem{}
	for _, it := range inv.Items {
		byItem[it.InvoiceItemID] = it
	}
	anyGRN := len(r.GRNIDs) > 0
	seenPOI := map[string]bool{}
	for _, a := range r.Allocations {
		var pi *model.POItem
		for _, p := range pool {
			if p.item.POItemID == a.POItemID {
				pi = p.item
			}
		}
		it := byItem[a.InvoiceItemID]
		ev := map[string]any{"invoice_line": a.InvoiceLine, "po_number": a.PONumber, "po_line": a.POLine}
		label := fmt.Sprintf("Invoice line %d vs %s line %d", a.InvoiceLine, a.PONumber, a.POLine)

		// PRICE_MATCH
		if it.UnitPrice != nil {
			diff := *it.UnitPrice - pi.UnitPrice
			allowed := math.Max(pi.UnitPrice*tol.PricePct/100, tol.PriceAbs)
			c := model.Check{Code: "PRICE_MATCH", Expected: money(pi.UnitPrice), Actual: money(*it.UnitPrice), Evidence: ev}
			switch {
			case math.Abs(diff) < 0.005:
				c.Status, c.Reason = model.Pass, label+": unit price matches the PO."
			case math.Abs(diff) <= allowed:
				c.Status, c.Reason = model.Pass, fmt.Sprintf("%s: unit price differs by %s, within tolerance (±%s).", label, money(diff), money(allowed))
			default:
				c.Status, c.Class, c.ExceptionCode = model.Fail, model.ClassBlocking, "PRICE_MISMATCH"
				c.Reason = fmt.Sprintf("%s: invoice charges %s per unit but the PO price is %s (difference %s, tolerance ±%s).",
					label, money(*it.UnitPrice), money(pi.UnitPrice), money(diff), money(allowed))
			}
			r.Checks = append(r.Checks, c)
		}
		// TAX_RATE_MATCH (kept separate from commercial price tolerance)
		if it.TaxRate != nil && pi.TaxRate > 0 {
			c := model.Check{Code: "TAX_RATE_MATCH", Expected: fmt.Sprintf("%g%%", pi.TaxRate), Actual: fmt.Sprintf("%g%%", *it.TaxRate), Evidence: ev}
			if math.Abs(*it.TaxRate-pi.TaxRate) < 0.01 {
				c.Status, c.Reason = model.Pass, label+": GST rate matches the PO."
			} else {
				c.Status, c.Class, c.ExceptionCode = model.Fail, model.ClassBlocking, "TAX_RATE_MISMATCH"
				c.Reason = fmt.Sprintf("%s: GST rate %g%% differs from the PO rate %g%%.", label, *it.TaxRate, pi.TaxRate)
			}
			r.Checks = append(r.Checks, c)
		}
		if seenPOI[pi.POItemID] {
			continue
		}
		seenPOI[pi.POItemID] = true
		inThis := thisQty[pi.POItemID]
		cum := pi.QuantityInvoicedPrior + inThis
		lines := lineOf[pi.POItemID]
		ev = map[string]any{"invoice_lines": lines, "po_number": a.PONumber, "po_line": a.POLine,
			"ordered": pi.QuantityOrdered, "previously_invoiced": pi.QuantityInvoicedPrior, "this_invoice": inThis, "accepted": pi.QuantityAccepted}
		label = fmt.Sprintf("%s line %d (%s)", a.PONumber, a.POLine, firstNonEmpty(pi.ItemCode, pi.Description))

		// QUANTITY_MATCH: cumulative invoiced vs ordered
		allowOrd := pi.QuantityOrdered * (1 + tol.QuantityPct/100)
		c := model.Check{Code: "QUANTITY_MATCH", Expected: "<= " + qty(pi.QuantityOrdered-pi.QuantityInvoicedPrior), Actual: qty(inThis), Evidence: ev}
		if cum <= allowOrd+1e-9 {
			c.Status = model.Pass
			c.Reason = fmt.Sprintf("%s: invoiced %s; cumulative %s of %s ordered.", label, qty(inThis), qty(cum), qty(pi.QuantityOrdered))
		} else {
			c.Status, c.Class, c.ExceptionCode = model.Fail, model.ClassBlocking, "QUANTITY_EXCEEDS_PO"
			c.Reason = fmt.Sprintf("%s: invoice bills %s but only %s remain on the PO (%s ordered, %s already invoiced).",
				label, qty(inThis), qty(math.Max(0, pi.QuantityOrdered-pi.QuantityInvoicedPrior)), qty(pi.QuantityOrdered), qty(pi.QuantityInvoicedPrior))
		}
		r.Checks = append(r.Checks, c)

		// GRN_ACCEPTED_QUANTITY: cumulative invoiced vs accepted (three-way)
		g := model.Check{Code: "GRN_ACCEPTED_QUANTITY", Expected: "<= " + qty(math.Max(0, pi.QuantityAccepted-pi.QuantityInvoicedPrior)), Actual: qty(inThis), Evidence: ev}
		allowAcc := pi.QuantityAccepted * (1 + tol.QuantityPct/100)
		switch {
		case !pi.HasReceipt:
			g.Status, g.Class, g.ExceptionCode = model.Fail, model.ClassPendingGRN, "GRN_NOT_RECEIVED"
			g.Reason = fmt.Sprintf("%s: no goods receipt has been recorded yet for this line.", label)
			if anyGRN {
				g.Reason = fmt.Sprintf("%s: goods receipts exist for the PO but none covers this line yet.", label)
			}
		case cum <= allowAcc+1e-9:
			g.Status = model.Pass
			g.Reason = fmt.Sprintf("%s: invoiced %s; cumulative %s within %s accepted.", label, qty(inThis), qty(cum), qty(pi.QuantityAccepted))
		default:
			g.Status, g.Class, g.ExceptionCode = model.Fail, model.ClassBlocking, "QUANTITY_EXCEEDS_GRN"
			g.Reason = fmt.Sprintf("%s: invoice bills %s but only %s remain to be billed against the GRN (%s accepted, %s already invoiced).",
				label, qty(inThis), qty(math.Max(0, pi.QuantityAccepted-pi.QuantityInvoicedPrior)), qty(pi.QuantityAccepted), qty(pi.QuantityInvoicedPrior))
		}
		r.Checks = append(r.Checks, g)
	}
	r.GRNIDs = uniq(r.GRNIDs)
	return r
}

// candidateSearch scores the vendor's open PO lines against each invoice line.
func candidateSearch(in Input, r *Result) []model.PurchaseOrder {
	inv := in.Invoice
	missing := ""
	if len(in.MissingRefs) > 0 {
		missing = " Referenced PO " + strings.Join(in.MissingRefs, ", ") + " is not on file."
	}
	type poScore struct {
		po      model.PurchaseOrder
		total   float64
		matched int
		why     []string
	}
	var scores []poScore
	for _, po := range in.VendorPOs {
		if in.RejectedPOIDs[po.PurchaseOrderID] || po.Status != "APPROVED" {
			continue
		}
		ps := poScore{po: po}
		for _, it := range inv.Items {
			best, why := 0.0, ""
			for _, pi := range po.Items {
				s, w := lineScore(it, pi)
				if it.Quantity != nil && *it.Quantity > pi.QuantityOrdered-pi.QuantityInvoicedPrior+1e-9 {
					s *= 0.8 // not enough remaining quantity
				}
				if s > best {
					best, why = s, w
				}
			}
			if best >= 0.6 {
				ps.matched++
				ps.why = append(ps.why, fmt.Sprintf("line %d ↔ %s", it.LineNumber, why))
			}
			ps.total += best
		}
		if inv.InvoiceDate != nil && po.PODate != nil && po.PODate.After(*inv.InvoiceDate) {
			ps.total *= 0.7
			ps.why = append(ps.why, "PO dated after the invoice")
		}
		if ps.matched > 0 {
			scores = append(scores, ps)
		}
	}
	n := float64(max(1, len(inv.Items)))
	sort.Slice(scores, func(a, b int) bool { return scores[a].total > scores[b].total })
	if len(scores) == 0 {
		r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Fail, Class: model.ClassPendingPO, ExceptionCode: "PO_NOT_FOUND",
			Actual: strings.Join(inv.POReferences, ", "),
			Reason: "No purchase order on file matches this invoice by reference, vendor, items, quantities or prices." + missing + " Procurement should investigate; a PO is never created automatically."})
		return nil
	}
	top := scores[0]
	conf := utils.Round2(top.total / n)
	coversAll := top.matched == len(inv.Items)
	ambiguous := len(scores) > 1 && scores[1].total >= top.total-0.15*n
	for i, s := range scores {
		if i > 0 && !ambiguous {
			break
		}
		if i >= 5 {
			break
		}
		r.POMatches = append(r.POMatches, POMatch{s.po.PurchaseOrderID, s.po.PONumber, MethodRule, StatusCandidate, utils.Round2(s.total / n),
			append([]string{"same vendor"}, s.why...)})
	}
	switch {
	case ambiguous:
		var nums []string
		for _, s := range scores[:min(5, len(scores))] {
			nums = append(nums, s.po.PONumber)
		}
		r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Warning, Class: model.ClassAmbiguous, ExceptionCode: "MULTIPLE_PO_CANDIDATES",
			Actual: strings.Join(nums, ", "), Reason: "Several open POs of this vendor fit the invoice equally well (" + strings.Join(nums, ", ") + "); AP must choose." + missing})
		return nil
	case coversAll && conf >= 0.9:
		r.POMatches[0].Status = StatusConfirmed
		r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Pass, Actual: top.po.PONumber,
			Evidence: map[string]any{"evidence": top.why, "confidence": conf},
			Reason:   fmt.Sprintf("No usable PO reference; %s is the only open PO of this vendor matching every line (%s).%s", top.po.PONumber, strings.Join(top.why, "; "), missing)})
		return []model.PurchaseOrder{top.po}
	default:
		r.Checks = append(r.Checks, model.Check{Code: "PO_MATCH", Status: model.Warning, Class: model.ClassAmbiguous, ExceptionCode: "PO_CANDIDATE_UNCONFIRMED",
			Actual: top.po.PONumber, Evidence: map[string]any{"evidence": top.why, "confidence": conf},
			Reason: fmt.Sprintf("Proposed %s (confidence %.0f%%) from vendor, items and prices, but the evidence is not strong enough to confirm automatically.%s", top.po.PONumber, conf*100, missing)})
		return nil
	}
}

func remaining(pi model.POItem, this map[string]float64) float64 {
	return pi.QuantityOrdered - pi.QuantityInvoicedPrior - this[pi.POItemID]
}

func hasCheck(cs []model.Check, code string) bool {
	for _, c := range cs {
		if c.Code == code {
			return true
		}
	}
	return false
}

func poNumbers(pos []model.PurchaseOrder) string {
	var s []string
	for _, p := range pos {
		s = append(s, p.PONumber)
	}
	return strings.Join(s, ", ")
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
