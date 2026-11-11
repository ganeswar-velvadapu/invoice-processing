package parse

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"invoiceproc/internal/classify"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
)

var (
	invNumLabels   = res(`\b(tax\s+)?invoice\s*((no|number|num|id|ref(erence)?)\b|#)\.?`, `\binv\.?\s*(no\b|#)\.?`, `\bbill\s*((no|number)\b|#)\.?`, `\bdocument\s*(no|number)\b\.?`)
	invDateLabels  = res(`\b(tax\s+)?invoice\s*date\b`, `\binv\.?\s*date\b`, `\bbill\s*date\b`, `\bdate\s*of\s*(invoice|issue)\b`, `\bissue\s*date\b`, `\bdated\b`)
	plainDateLabel = res(`(?:^|\s)date\b`)
	dueDateLabels  = res(`\bdue\s*date\b`, `\bpayment\s*due(\s*date)?\b`, `\bdue\s*on\b`)
	poRefLabels    = res(`\b(p\.?\s?o\.?|purchase\s*order|buyer'?s?\s*order|order)\s*(no|number|ref(erence)?|#|nos)\b\.?\s*(\(s\))?`, `\bpo\s*ref\b\.?`,
		`\brequisition\s*(no|number)\b\.?`, `\bagainst\s*(p\.?o\.?|purchase\s*order)\b`, `\bp\.?o\.?\s*#`, `(?:^|\s)p\.?o\.?\s*:`)
	poNumLabels    = res(`\b(p\.?\s?o\.?|purchase\s*order)\s*(no|number|#|num)\b\.?`, `\border\s*(no|number|reference|ref)\b\.?`, `\brequisition\s*(no|number)\b\.?`, `\bpo\s*ref\b\.?`)
	poDateLabels   = res(`\b(p\.?\s?o\.?|purchase\s*order|order)\s*date\b`, `\bdate\s*of\s*order\b`, `\bissue\s*date\b`, `\bdated\b`)
	grnNumLabels   = res(`\b(grn|goods\s*receipt(\s*note)?|receipt|mrn|inward)\s*(no|number|#)\b\.?`, `\bgrn\b\s*[:#]`)
	invNumPhrases  = []string{"tax invoice no", "invoice number", "invoice no", "invoice ref", "bill no", "invoice id"}
	invDatePhrases = []string{"invoice date", "bill date", "date of invoice", "issue date"}
	poNumPhrases   = []string{"purchase order no", "po number", "po no", "order reference", "po ref", "requisition no", "order no"}
	poRefPhrases   = []string{"purchase order no", "po number", "po no", "order reference", "po ref", "requisition no", "against po"}
	poDatePhrases  = []string{"po date", "order date", "issue date", "date of order"}
	grnTokenRe     = regexp.MustCompile(`(?i)\b(GRN|MRN|GR)[\s/:#-]*[A-Z0-9][A-Z0-9/\-]*\d[A-Z0-9/\-]*`)
	grnDateLabels  = res(`\b(grn|receipt|received|inward)\s*date\b`, `\bdate\s*of\s*receipt\b`)
	termsLabels    = res(`\bpayment\s*terms\b`, `\bterms\s*of\s*payment\b`, `\bcredit\s*(terms|period)\b`)

	sellerCtx = regexp.MustCompile(`(?i)\b(seller|supplier|vendor|from|sold\s*by|billed\s*by|issued\s*by|manufacturer|consignor)\b`)
	buyerCtx  = regexp.MustCompile(`(?i)\b(buyer|bill(ed)?\s*to|billed\s*party|client|ship(ped)?\s*to|customer|consignee|recipient|sold\s*to|deliver\s*to|invoice\s*to|purchaser)\b`)
	// On a PO "From" is ambiguous (issuer or vendor, depending on the form): it is left to GSTIN/master-data resolution.
	poVendorCtx      = regexp.MustCompile(`(?i)\b(vendor|supplier|seller|sold\s*by|issued\s*by|m/s)\b|^\s*to\b`)
	poBuyerCtx       = regexp.MustCompile(`(?i)\b(buyer|bill(ed)?\s*to|billed\s*party|client|consignee|ship(ped)?\s*to|deliver(y)?\s*(to|address|at)|dispatch\s*to|consign\s*to|invoice\s*to|purchaser)\b`)
	titleRe          = regexp.MustCompile(`(?i)^\s*(tax\s+invoice|invoice|bill\s+of\s+supply|purchase\s+order|goods\s+receipt\s+note|grn|original|duplicate|triplicate|e-?invoice|.*for\s+recipient|.*copy)\s*$`)
	poTokenRe        = regexp.MustCompile(`(?i)\bP\.?O[-/ ]?[A-Z0-9]*\d[A-Z0-9\-/]*\b`)
	currRe           = regexp.MustCompile(`(?i)(₹|\bINR\b|\bRs\.?|\brupees\b)|(\bUSD\b|US\$|\$)|(\bEUR\b|€)`)
	gstinLabelRe     = regexp.MustCompile(`(?i)\bGST(IN)?\s*(/\s*UIN)?\s*(no\.?|number)?\s*[:.\-]?\s*`)
	parenLabelRe     = regexp.MustCompile(`^\s*\([^)]{1,30}\)`)
	sellerSqueezed   = regexp.MustCompile(`(seller|supplier|vendor|soldby|billedby|issuedby|consignor)`)
	poVendorSqueezed = regexp.MustCompile(`(vendor|supplier|seller|soldby|issuedby)`)
	buyerSqueezed    = regexp.MustCompile(`(buyer|billto|billedto|billedparty|shipto|customer|consignee|recipient|soldto|deliverto|invoiceto|purchaser|client)`)
	gstTermsRe       = regexp.MustCompile(`(?i)\b(gstin|cgst|sgst|igst|hsn)\b`)
	labelish         = regexp.MustCompile(`(?i)^(gstin|gst\s*no|pan|state|phone|ph|tel|email|e-mail|cin|address|place\s+of\s+supply|dated?|invoice|bill\s*no|po\b|order|issue\s*date|due\s*date)\b`)
)

// Parse extracts header fields and items for the given document type.
func Parse(lines []extract.Line, docType string, opt Options) *Data {
	d := &doc{lines: lines}
	out := &Data{DocumentType: docType, Fields: map[string]*Field{}}
	items, tStart, tEnd := d.parseTable(docType)
	out.Items = items
	inTable := func(i int) bool { return len(items) > 0 && i > tStart && i < tEnd }
	set := func(name string, f *Field) {
		if f != nil && f.Value != "" {
			out.Fields[name] = f
		}
	}

	look := func(labels []*regexp.Regexp, phrases []string, val valueFn) *Field {
		if m := d.findLabeled(labels, val, 0, 0, inTable); m != nil {
			return d.field(m, "anchor")
		}
		if m := d.findFuzzy(phrases, val, inTable); m != nil {
			return d.field(m, "anchor_fuzzy")
		}
		return nil
	}
	sellerG, buyerG := d.gstins(opt, sellerCtx, buyerCtx, true)
	switch docType {
	case "INVOICE":
		set("invoice_number", look(invNumLabels, invNumPhrases, docNumberValue))
		set("invoice_date", look(invDateLabels, invDatePhrases, dateValue))
		if out.Fields["invoice_date"] == nil {
			if m := d.findLabeled(plainDateLabel, dateValue, 0, 0, func(i int) bool {
				return inTable(i) || regexp.MustCompile(`(?i)(due|order|po|delivery|receipt|challan|ack|e-?way)\s*date`).MatchString(d.lines[i].Text)
			}); m != nil {
				m.conf *= 0.85
				set("invoice_date", d.field(m, "anchor"))
			}
		}
		set("due_date", look(dueDateLabels, []string{"due date", "payment due"}, dateValue))
		set("po_reference", d.poReferences(inTable))
		set("seller_gstin", sellerG)
		set("buyer_gstin", buyerG)
		set("seller_name", d.partyName(sellerCtx, sellerG, true))
		set("buyer_name", d.partyName(buyerCtx, buyerG, false))
		set("payment_terms", look(termsLabels, []string{"payment terms"}, textValue))
		set("currency", d.currency())
		d.totals(out, tEnd)
	case "PO":
		set("po_number", look(poNumLabels, poNumPhrases, docNumberValue))
		if out.Fields["po_number"] == nil {
			set("po_number", d.poTokenFallback(inTable))
		}
		set("po_date", look(poDateLabels, poDatePhrases, dateValue))
		if out.Fields["po_date"] == nil {
			set("po_date", d.field(d.findLabeled(plainDateLabel, dateValue, 0, 0, inTable), "anchor"))
		}
		// On a PO the issuing party is the buyer; the vendor is the "To/Supplier/Vendor" party.
		vendorG, ourG := d.gstins(opt, poVendorCtx, poBuyerCtx, false)
		set("vendor_gstin", vendorG)
		set("buyer_gstin", ourG)
		set("vendor_name", d.partyName(poVendorCtx, vendorG, false))
		set("buyer_name", d.partyName(poBuyerCtx, ourG, false))
		set("payment_terms", look(termsLabels, []string{"payment terms"}, textValue))
		set("currency", d.currency())
		if m := d.findLabeled(res(`\b(grand\s*total|total\s*(po\s*)?(amount|value)|po\s*value|net\s*total|total)\b`), lastAmount, tEnd, 0, nil); m != nil {
			set("total_amount", d.field(m, "anchor"))
		}
	case "GRN":
		set("grn_number", look(grnNumLabels, []string{"grn no", "grn number", "receipt no", "receipt number"}, docNumberValue))
		if out.Fields["grn_number"] == nil {
			set("grn_number", d.tokenFallback(grnTokenRe, inTable))
		}
		set("receipt_date", look(grnDateLabels, []string{"receipt date", "grn date", "received on", "date of receipt"}, dateValue))
		if out.Fields["receipt_date"] == nil {
			set("receipt_date", d.field(d.findLabeled(plainDateLabel, dateValue, 0, 0, inTable), "anchor"))
		}
		set("po_reference", d.poReferences(inTable))
		set("vendor_gstin", sellerG)
		set("vendor_name", d.partyName(regexp.MustCompile(`(?i)\b(vendor|supplier|seller|received\s*from)\b`), sellerG, false))
		set("invoice_reference", d.field(d.findLabeled(res(`\b(invoice|challan|dc|delivery\s*challan)\s*(no|number|#|ref)?\b\.?\s*:?`), docNumberValue, 0, 0, inTable), "anchor"))
	}

	d.applyProfile(out, opt.Profile)
	if docType == "INVOICE" {
		ocr := false
		for _, l := range lines {
			if l.Conf > 0 {
				ocr = true
				break
			}
		}
		if ocr {
			repairTotalsGlyph(out)
		}
		crossCheckTotals(out)
	}
	return out
}

// gstins finds all GSTINs and splits them into seller (vendor) / buyer.
// Candidates come from the GSTIN pattern and from the text after a "GSTIN" label (OCR often breaks
// the pattern). Each is checksum-repaired, or snapped to a known GSTIN (vendor master / our own)
// when within a small edit distance. Roles come from nearby labels, then from master data,
// then from position (issuerIsSeller: an unlabelled letterhead GSTIN is the seller's on an invoice
// and the buyer's on a PO).
func (d *doc) gstins(opt Options, sellerRe, buyerRe *regexp.Regexp, issuerIsSeller bool) (seller, buyer *Field) {
	type cand struct {
		g      string
		line   int
		col    int
		role   int // +1 seller/vendor, -1 buyer, 0 unknown
		valid  bool
		repair bool
		snap   bool
	}
	ourSet, vendorSet := map[string]bool{}, map[string]bool{}
	var known []string
	for _, g := range opt.BuyerGSTINs {
		if g = gstin.Normalize(g); g != "" {
			ourSet[g] = true
			known = append(known, g)
		}
	}
	for _, g := range opt.VendorGSTINs {
		if g = gstin.Normalize(g); g != "" {
			vendorSet[g] = true
			known = append(known, g)
		}
	}
	var cs []cand
	seen := map[string]bool{}
	add := func(raw string, line, col int) {
		g, repaired := gstin.Repair(raw)
		chk, _ := gstin.Check(g)
		c := cand{g: g, line: line, col: col, valid: chk.Valid, repair: repaired}
		if !c.valid || (len(known) > 0 && !ourSet[g] && !vendorSet[g]) {
			if k, ok := SnapGSTIN(raw, known); ok {
				c.g, c.valid, c.snap = k, true, true
			}
		}
		if !c.valid && len(c.g) != 15 {
			return
		}
		if seen[c.g] {
			return
		}
		seen[c.g] = true
		cs = append(cs, c)
	}
	for i, l := range d.lines {
		up := strings.ToUpper(l.Text)
		found := false
		for _, loc := range gstin.FindRe.FindAllStringIndex(up, -1) {
			add(up[loc[0]:loc[1]], i, len([]rune(l.Text[:loc[0]])))
			found = true
		}
		if found {
			continue
		}
		// "GSTIN: 101J6TD6056N #2]" — gather the characters after the label
		for _, loc := range gstinLabelRe.FindAllStringIndex(l.Text, -1) {
			rest := []rune(l.Text[loc[1]:])
			var b strings.Builder
			for _, r := range rest[:min(len(rest), 24)] {
				if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
					b.WriteRune(r)
				} else if r == ' ' && b.Len() >= 15 {
					break
				}
			}
			if raw := strings.ToUpper(b.String()); len(raw) >= 12 {
				if len(raw) > 17 {
					raw = raw[:17]
				}
				add(raw, i, len([]rune(l.Text[:loc[0]])))
			}
		}
	}
	for k := range cs {
		c := &cs[k]
		// Look back up to 6 lines (and the left part of this line) for role context, nearest first.
		for j := c.line; j >= 0 && j >= c.line-6; j-- {
			r := []rune(d.lines[j].Text)
			lo, hi := 0, len(r)
			if c.col > 40 { // two-column layouts: stay in the same half of the page
				lo = min(len(r), max(0, c.col-20))
			} else {
				hi = min(len(r), c.col+40)
			}
			seg := string(r[lo:hi])
			// letter-spaced headings ("PU R CHAS E R", "V E NDOR") only match with spaces removed
			squeezed := strings.ToLower(strings.ReplaceAll(seg, " ", ""))
			sb := sellerRe.MatchString(seg) || (sellerRe == sellerCtx && sellerSqueezed.MatchString(squeezed)) || (sellerRe == poVendorCtx && poVendorSqueezed.MatchString(squeezed))
			bb := buyerRe.MatchString(seg) || buyerSqueezed.MatchString(squeezed)
			if bb && !sb {
				c.role = -1
				break
			}
			if sb && !bb {
				c.role = 1
				break
			}
		}
		// master data is stronger than layout guesses
		if ourSet[c.g] {
			c.role = -1
		} else if vendorSet[c.g] {
			c.role = 1
		}
	}
	mk := func(c cand, conf float64) *Field {
		src := "anchor"
		switch {
		case !c.valid:
			conf = math.Min(conf, 0.4)
		case c.snap:
			conf, src = math.Min(conf, 0.8), "anchor+master_snap"
		case c.repair:
			conf = math.Min(conf, 0.85)
		}
		l := d.lines[c.line]
		return &Field{Value: c.g, Confidence: round4(ocrFactor(l, conf)), Page: l.Page, BBox: l.BBox, Source: src}
	}
	labelledBuyer := false
	for _, c := range cs {
		if c.role == -1 {
			labelledBuyer = true
		}
	}
	for _, c := range cs {
		if c.role == 1 && seller == nil {
			seller = mk(c, 0.95)
		}
		if c.role == -1 && buyer == nil {
			buyer = mk(c, 0.93)
		}
	}
	// Unlabelled: the first GSTIN printed is normally the issuer's letterhead.
	for _, c := range cs {
		if (seller != nil && c.g == seller.Value) || (buyer != nil && c.g == buyer.Value) {
			continue
		}
		if issuerIsSeller {
			if seller == nil && c.role != -1 {
				conf := 0.75
				if labelledBuyer {
					conf = 0.88 // the other GSTIN is clearly the buyer's
				}
				seller = mk(c, conf)
			} else if buyer == nil {
				buyer = mk(c, 0.7)
			}
		} else {
			if buyer == nil && c.role != 1 {
				buyer = mk(c, 0.7)
			} else if seller == nil {
				seller = mk(c, 0.75)
			}
		}
	}
	return
}

// SnapGSTIN maps an OCR-damaged GSTIN to the unique known GSTIN within edit distance 3.
func SnapGSTIN(raw string, known []string) (string, bool) {
	raw = strings.ToUpper(raw)
	best, bestD, second := "", 99, 99
	for _, k := range known {
		dd := classify.Levenshtein(raw, k)
		if len(raw) > 15 { // trailing OCR junk: also compare the first 15 characters
			dd = min(dd, classify.Levenshtein(raw[:15], k))
		}
		if dd < bestD {
			best, second, bestD = k, bestD, dd
		} else if dd < second {
			second = dd
		}
	}
	if best != "" && bestD <= 3 && second > bestD+1 {
		return best, true
	}
	return "", false
}

// partyName finds a party name under/after a role label, else (for the issuer) the first top line.
func (d *doc) partyName(ctx *regexp.Regexp, g *Field, issuer bool) *Field {
	// Block headings, possibly letter-spaced ("V E NDOR", "PU R CHAS E R"): the name is the next line
	// under the heading's column.
	squeezedRe := sellerSqueezed
	switch ctx {
	case buyerCtx, poBuyerCtx:
		squeezedRe = buyerSqueezed
	case poVendorCtx:
		squeezedRe = poVendorSqueezed
	}
	for i, l := range d.lines {
		if i > 60 || i+1 >= len(d.lines) {
			break
		}
		for _, c := range cells(l.Text) {
			sq := strings.ToLower(strings.ReplaceAll(c.text, " ", ""))
			if len(sq) > 22 || !squeezedRe.MatchString(sq) || strings.ContainsAny(sq, "0123456789:") {
				continue // a heading is a short cell of its own, not a label with a value
			}
			for j := i + 1; j <= i+2 && j < len(d.lines); j++ {
				nr := []rune(d.lines[j].Text)
				first := 0
				for first < len(nr) && nr[first] == ' ' {
					first++
				}
				// the name must start under the heading, not somewhere further along the line
				if first < c.start-4 || first > c.start+6 {
					continue
				}
				cand := nameValue(string(nr[first:]))
				if cand != "" && len(cand) >= 3 && !labelish.MatchString(cand) && !strings.ContainsAny(cand[:1], "0123456789") &&
					!gstin.FindRe.MatchString(strings.ToUpper(cand)) && !DateRe.MatchString(cand) && !isAllCaps(cand) {
					return &Field{Value: cand, Confidence: round4(ocrFactor(d.lines[j], 0.75)), Page: d.lines[j].Page, Source: "anchor"}
				}
			}
		}
	}
	for i, l := range d.lines {
		if i > 60 {
			break
		}
		loc := ctx.FindStringIndex(l.Text)
		if loc == nil {
			continue
		}
		rawAfter := l.Text[loc[1]:]
		// "SUPPLIER (CONSIGNOR)": a parenthesised label qualifier is not the name
		rawAfter = parenLabelRe.ReplaceAllString(rawAfter, "")
		// The value normally sits right after the label. After a wide gap it may be the next column's
		// label ("Payment Terms: Net 30") or a key-value layout's value ("Supplier      Bava, Bora and Wason"):
		// accept it only if it is not itself a "label: value" pair.
		cand := nameValue(strings.TrimLeft(rawAfter, ":-–#)( "))
		if i := strings.Index(cand, " | "); i > 0 {
			cand = strings.TrimSpace(cand[:i])
		}
		// after a wide gap, a "label: value" pair or an ALL-CAPS heading is the next column, not the name
		if lead := len(rawAfter) - len(strings.TrimLeft(rawAfter, " :-–#")); lead > 4 && (strings.Contains(cand, ":") || isAllCaps(cand)) {
			cand = ""
		}
		if cand == "" || ctx.MatchString(cand) || labelish.MatchString(cand) || len(cand) < 3 || gstin.FindRe.MatchString(strings.ToUpper(cand)) {
			// name on one of the next lines, starting in the same column
			cand = ""
			col := len([]rune(l.Text[:loc[0]]))
			for j := i + 1; j <= i+2 && j < len(d.lines) && cand == ""; j++ {
				r := []rune(d.lines[j].Text)
				if col >= len(r) {
					continue
				}
				seg := string(r[max(0, col-2):])
				if lead := len(seg) - len(strings.TrimLeft(seg, " ")); lead > 6 {
					continue
				}
				cand = nameValue(seg)
			}
		}
		if cand != "" && len(cand) >= 3 && !labelish.MatchString(cand) && !strings.ContainsAny(cand[:1], "0123456789") &&
			!gstin.FindRe.MatchString(strings.ToUpper(cand)) && !DateRe.MatchString(cand) {
			return &Field{Value: cand, Confidence: round4(ocrFactor(l, 0.7)), Page: l.Page, Source: "anchor"}
		}
	}
	if issuer {
		for i, l := range d.lines {
			if i > 8 {
				break
			}
			t := nameValue(l.Text)
			whole := strings.Join(strings.Fields(l.Text), " ")
			if t == "" || titleRe.MatchString(t) || titleRe.MatchString(whole) || labelish.MatchString(t) || len(t) < 3 {
				continue
			}
			return &Field{Value: t, Confidence: round4(ocrFactor(l, 0.55)), Page: l.Page, Source: "fallback"}
		}
	}
	return nil
}

func (d *doc) poReferences(skip func(int) bool) *Field {
	for _, re := range poRefLabels {
		for i, l := range d.lines {
			if skip(i) {
				continue
			}
			loc := re.FindStringIndex(l.Text)
			if loc == nil {
				continue
			}
			rest := l.Text[loc[1]:]
			rest = rest[len(sepRe.FindString(rest)):]
			seg := gapRe.Split(strings.TrimSpace(rest), 2)[0]
			var refs []string
			for _, part := range regexp.MustCompile(`\s*(,|&|;|\band\b|\s/\s)\s*`).Split(seg, -1) {
				if v := docNumberValue(part); v != "" {
					refs = append(refs, v)
				}
			}
			if len(refs) == 0 && i+1 < len(d.lines) {
				col := len([]rune(l.Text[:loc[0]]))
				r := []rune(d.lines[i+1].Text)
				if col < len(r) {
					if v := docNumberValue(fromColumn(string(r), col)); v != "" {
						refs = append(refs, v)
						i++
					}
				}
			}
			if len(refs) > 0 {
				ll := d.lines[i]
				return &Field{Value: strings.Join(dedupe(refs), ","), Confidence: round4(ocrFactor(ll, 0.9)), Page: ll.Page, Source: "anchor"}
			}
		}
	}
	if m := d.findFuzzy(poRefPhrases, docNumberValue, skip); m != nil {
		return d.field(m, "anchor_fuzzy")
	}
	return d.poTokenFallback(skip)
}

func (d *doc) poTokenFallback(skip func(int) bool) *Field {
	var refs []string
	page, line := 0, -1
	for i, l := range d.lines {
		if skip(i) {
			continue
		}
		for _, m := range poTokenRe.FindAllString(l.Text, -1) {
			refs = append(refs, m)
			if line < 0 {
				line, page = i, l.Page
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return &Field{Value: strings.Join(dedupe(refs), ","), Confidence: round4(ocrFactor(d.lines[line], 0.6)), Page: page, Source: "fallback"}
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		k := strings.ToUpper(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

func (d *doc) currency() *Field {
	counts := [4]int{}
	gst := false
	for _, l := range d.lines {
		if gstTermsRe.MatchString(l.Text) {
			gst = true
		}
		for _, m := range currRe.FindAllStringSubmatch(l.Text, -1) {
			for k := 1; k <= 3; k++ {
				if m[k] != "" {
					counts[k]++
				}
			}
		}
	}
	// A bare "$" is common OCR noise; foreign currency needs an explicit code, and GST documents default to INR.
	if counts[2] > counts[1] && explicitUSD(d) && !(gst && counts[1] > 0) {
		return &Field{Value: "USD", Confidence: 0.8, Source: "anchor"}
	}
	if counts[3] > counts[1] && !gst {
		return &Field{Value: "EUR", Confidence: 0.8, Source: "anchor"}
	}
	if counts[1] > 0 {
		return &Field{Value: "INR", Confidence: 0.92, Source: "anchor"}
	}
	conf := 0.6
	if gst {
		conf = 0.85
	}
	return &Field{Value: "INR", Confidence: conf, Source: "fallback"}
}

func explicitUSD(d *doc) bool {
	for _, l := range d.lines {
		if strings.Contains(strings.ToUpper(l.Text), "USD") || strings.Contains(l.Text, "US$") {
			return true
		}
	}
	return false
}

var totalLabels = map[string][]*regexp.Regexp{
	"subtotal":       res(`\bsub\s*-?\s*total\b`),
	"taxable_amount": res(`\b(total\s*)?taxable\s*(value|amount|amt)\b`),
	"cgst":           res(`\b(total\s*)?cgst\b`),
	"sgst":           res(`\b(total\s*)?(sgst|utgst)\b`),
	"igst":           res(`\b(total\s*)?igst\b`),
	"total_tax":      res(`\btotal\s*(tax|gst)(\s*amount)?\b`),
	"other_charges":  res(`\b(freight|shipping|packing|packaging|forwarding|handling|other\s*charges|transport(ation)?)\b(\s*charges)?`),
	"round_off":      res(`\bround(ed|ing)?\s*-?\s*off\b|\bround\s*off\b`),
	"grand_total": res(`\bgrand\s*total\b`, `\btotal\s*invoice\s*(value|amount)\b`, `\b(net\s*)?amount\s*payable\b`,
		`\b(total\s*)?payable\s*amount\b`, `\binvoice\s*(total|value)\b`, `\btotal\s*amount(\s*due)?\b`, `\btotal\s*due\b`, `\bbalance\s*due\b`, `\bnet\s*(total|amount)\b`, `^\s*total\b`),
}

var notGrand = regexp.MustCompile(`(?i)\b(taxable|sub\s*-?\s*total|total\s*(tax|gst|qty|quantity|cgst|sgst|igst)|in\s*words)\b`)
var taxHeaderRe = regexp.MustCompile(`(?i)\b(c|s|i|ut)gst\s*(rate|amt\b|amount\b)`)
var wordsRe = regexp.MustCompile(`(?i)(rupees|only\b|in\s*words)`)

// totals reads summary amounts after the items table (bottom-up for grand total).
func (d *doc) totals(out *Data, tableEnd int) {
	from := tableEnd
	if from <= 0 || from >= len(d.lines) {
		from = len(d.lines) / 3
	}
	skipWords := func(i int) bool { return wordsRe.MatchString(d.lines[i].Text) }
	for _, name := range []string{"subtotal", "taxable_amount", "cgst", "sgst", "igst", "total_tax", "other_charges", "round_off"} {
		if name == "cgst" || name == "sgst" || name == "igst" {
			if f := d.taxTotal(name, from, skipWords); f != nil {
				out.Fields[name] = f
				continue
			}
		}
		skip := skipWords
		if name == "cgst" || name == "sgst" || name == "igst" {
			skip = func(i int) bool { return skipWords(i) || taxHeaderRe.MatchString(d.lines[i].Text) }
		}
		if m := d.findLabeled(totalLabels[name], lastAmount, from, 0, skip); m != nil {
			if name == "round_off" { // the label/value separator swallows a leading minus
				if v := signedLastAmount(d.lines[m.line].Text); v != "" {
					m.value = v
				}
			}
			// "CGST @ 9%" without an amount would yield 9 — require the line to contain more than a rate
			if (name == "cgst" || name == "sgst" || name == "igst") && strings.Contains(d.lines[m.line].Text, "%") {
				nums := numberRe.FindAllString(d.lines[m.line].Text, -1)
				if len(nums) < 2 {
					continue
				}
			}
			out.Fields[name] = d.field(m, "anchor")
		}
	}
	for _, re := range totalLabels["grand_total"] {
		for i := len(d.lines) - 1; i >= from; i-- {
			t := d.lines[i].Text
			if notGrand.MatchString(t) && !regexp.MustCompile(`(?i)grand\s*total|payable`).MatchString(t) {
				continue
			}
			if wordsRe.MatchString(t) {
				continue
			}
			if re.MatchString(t) {
				if v := lastAmount(t); v != "" {
					out.Fields["grand_total"] = d.field(&match{value: v, line: i, conf: ocrFactor(d.lines[i], 0.85)}, "anchor")
					return
				}
			}
		}
	}
}

// taxTotal handles a tax split by rate ("IGST @ 12%  13,920.00", "IGST @ 18%  59,373.00"): an explicit
// "Total IGST" line wins; otherwise several rate lines are summed. Returns nil for the plain single-line case.
func (d *doc) taxTotal(name string, from int, skip func(int) bool) *Field {
	label := regexp.MustCompile(`(?i)^\s*(total\s*)?` + name + `\b`)
	totalRe := regexp.MustCompile(`(?i)^\s*total\s*` + name + `\b`)
	rateRe := regexp.MustCompile(`(?i)^\s*` + name + `\s*@?\s*\d+(\.\d+)?\s*%`)
	var sum float64
	var rateLines []int
	for i := from; i < len(d.lines); i++ {
		if skip != nil && skip(i) {
			continue
		}
		t := d.lines[i].Text
		if !label.MatchString(t) {
			continue
		}
		v := lastAmount(t)
		if v == "" {
			continue
		}
		if totalRe.MatchString(t) {
			return d.field(&match{value: v, line: i, conf: ocrFactor(d.lines[i], 0.92)}, "anchor")
		}
		if rateRe.MatchString(t) && len(numberRe.FindAllString(t, -1)) >= 2 {
			f, _ := ParseNumber(v)
			sum += f
			rateLines = append(rateLines, i)
		}
	}
	if len(rateLines) < 2 {
		return nil
	}
	return d.field(&match{value: fmtNum(math.Round(sum*100) / 100), line: rateLines[0], conf: ocrFactor(d.lines[rateLines[0]], 0.88)}, "anchor")
}

func fval(out *Data, k string) (float64, bool) {
	f := out.Fields[k]
	if f == nil {
		return 0, false
	}
	return ParseNumber(f.Value)
}

func setComputed(out *Data, k string, v float64, conf float64) {
	out.Fields[k] = &Field{Value: fmtNum(math.Round(v*100) / 100), Confidence: conf, Source: "computed"}
}

// crossCheckTotals derives missing totals from items and boosts confidence when arithmetic agrees.
func crossCheckTotals(out *Data) {
	var itemTaxable, itemTax float64
	allTaxable, allTax := len(out.Items) > 0, len(out.Items) > 0
	for _, it := range out.Items {
		if it.TaxableValue != nil {
			itemTaxable += *it.TaxableValue
		} else {
			allTaxable = false
		}
		if it.TaxAmount != nil {
			itemTax += *it.TaxAmount
		} else {
			allTax = false
		}
	}
	if _, ok := fval(out, "taxable_amount"); !ok {
		if v, ok := fval(out, "subtotal"); ok {
			setComputed(out, "taxable_amount", v, out.Fields["subtotal"].Confidence)
		} else if allTaxable {
			setComputed(out, "taxable_amount", itemTaxable, 0.7)
		}
	}
	if _, ok := fval(out, "subtotal"); !ok {
		if v, ok := fval(out, "taxable_amount"); ok {
			setComputed(out, "subtotal", v, out.Fields["taxable_amount"].Confidence)
		}
	}
	cg, _ := fval(out, "cgst")
	sg, _ := fval(out, "sgst")
	ig, _ := fval(out, "igst")
	if _, ok := fval(out, "total_tax"); !ok {
		if cg+sg+ig > 0 {
			setComputed(out, "total_tax", cg+sg+ig, 0.85)
		} else if allTax {
			setComputed(out, "total_tax", itemTax, 0.65)
		}
	}
	taxable, okT := fval(out, "taxable_amount")
	tax, okX := fval(out, "total_tax")
	// Invoice-level tax only (no per-line rate column): propagate a uniform standard GST rate to lines.
	if okT && okX && taxable > 0 && len(out.Items) > 0 {
		missing := true
		for _, it := range out.Items {
			if it.TaxRate != nil {
				missing = false
			}
		}
		if missing {
			rate := math.Round(tax/taxable*10000) / 100
			for _, std := range []float64{0, 0.25, 3, 5, 12, 18, 28, 40} {
				if math.Abs(rate-std) < 0.02 {
					for k := range out.Items {
						it := &out.Items[k]
						r := std
						it.TaxRate = &r
						if it.TaxableValue != nil {
							ta := math.Round(*it.TaxableValue*std) / 100
							it.TaxAmount = &ta
							lt := *it.TaxableValue + ta
							it.LineTotal = &lt
						}
					}
					break
				}
			}
		}
	}
	other, _ := fval(out, "other_charges")
	ro, _ := fval(out, "round_off")
	gt, okG := fval(out, "grand_total")
	if okT && okX {
		exp := taxable + tax + other + ro
		if okG && math.Abs(exp-gt) <= 1.0 {
			for _, k := range []string{"taxable_amount", "total_tax", "grand_total", "subtotal", "cgst", "sgst", "igst"} {
				if f := out.Fields[k]; f != nil && f.Confidence < 0.97 {
					f.Confidence = math.Max(f.Confidence, 0.97)
				}
			}
		} else if !okG {
			setComputed(out, "grand_total", exp, 0.6)
			out.Warnings = append(out.Warnings, "grand total not found; computed from taxable value and tax")
		} else if okG {
			out.Warnings = append(out.Warnings, "printed totals do not add up: check taxable, tax and grand total")
			// Maybe "grand_total" picked a wrong line: keep, but lower confidence so it goes to review.
			if f := out.Fields["grand_total"]; f != nil {
				f.Confidence = math.Min(f.Confidence, 0.6)
			}
		}
	}
	if allTaxable && okT && math.Abs(itemTaxable-taxable) > 1.0 {
		out.Warnings = append(out.Warnings, "sum of line taxable values differs from the taxable total")
	}
}

// applyProfile applies vendor-specific regex overrides (one capture group each).
func (d *doc) applyProfile(out *Data, p Profile) {
	if len(p) == 0 {
		return
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		re, err := regexp.Compile(p[k])
		if err != nil {
			out.Warnings = append(out.Warnings, "invalid profile pattern for "+k)
			continue
		}
		for _, l := range d.lines {
			m := re.FindStringSubmatch(l.Text)
			if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
				continue
			}
			v := strings.TrimSpace(m[1])
			if strings.HasSuffix(k, "_date") || k == "invoice_date" {
				if t, ok := ParseDate(v); ok {
					v = t.Format("2006-01-02")
				}
			} else if f, ok := ParseNumber(v); ok && isAmountField(k) {
				v = fmtNum(f)
			}
			out.Fields[k] = &Field{Value: v, Confidence: round4(ocrFactor(l, 0.95)), Page: l.Page, BBox: l.BBox, Source: "profile"}
			break
		}
	}
}

func isAmountField(k string) bool {
	switch k {
	case "subtotal", "taxable_amount", "cgst", "sgst", "igst", "total_tax", "other_charges", "round_off", "grand_total", "total_amount":
		return true
	}
	return false
}

// LowConfidence lists critical fields that are missing or below threshold, plus weak items.
func LowConfidence(data *Data, threshold float64) []string {
	var out []string
	if data.DocumentType == "OTHER" || (data.ClassificationConfidence > 0 && data.ClassificationConfidence < 0.6) {
		out = append(out, "document_type")
	}
	for _, k := range CriticalFields[data.DocumentType] {
		f := data.Fields[k]
		if f == nil || f.Value == "" || f.Confidence < threshold {
			out = append(out, k)
		}
	}
	if len(data.Items) == 0 && data.DocumentType != "OTHER" {
		out = append(out, "items")
	}
	for _, it := range data.Items {
		if it.Confidence < threshold {
			out = append(out, "items["+itoa(it.LineNumber)+"]")
		}
	}
	return out
}

// OverallConfidence is the minimum over critical fields and the mean item confidence.
func OverallConfidence(data *Data) float64 {
	c := 1.0
	if data.DocumentType == "OTHER" {
		return round4(math.Min(data.ClassificationConfidence, 0.3))
	}
	if data.ClassificationConfidence > 0 {
		c = math.Min(c, data.ClassificationConfidence)
	}
	for _, k := range CriticalFields[data.DocumentType] {
		f := data.Fields[k]
		if f == nil {
			return 0
		}
		c = math.Min(c, f.Confidence)
	}
	if n := len(data.Items); n > 0 {
		var s float64
		for _, it := range data.Items {
			s += it.Confidence
		}
		c = math.Min(c, s/float64(n))
	} else if data.DocumentType == "INVOICE" || data.DocumentType == "PO" || data.DocumentType == "GRN" {
		c = math.Min(c, 0.3)
	}
	return round4(c)
}

func itoa(i int) string { return fmtNum(float64(i)) }

// repairTotalsGlyph fixes a rupee sign OCR'd as a leading digit in the totals block
// ("₹12.98" -> "212.98"). Among the variants (as read / leading 2,7,5 dropped) of taxable value,
// total tax and grand total it picks the combination that adds up, preferring a tax that is a
// standard GST rate of the taxable value and a taxable value equal to the sum of the lines.
func repairTotalsGlyph(out *Data) {
	get := func(k string) string {
		if f := out.Fields[k]; f != nil {
			return f.Value
		}
		return ""
	}
	variants := func(v string) []float64 {
		var r []float64
		if f, ok := ParseNumber(v); ok {
			r = append(r, f)
			if len(v) >= 2 && strings.ContainsRune(glyphDigits, rune(v[0])) {
				if g, ok := ParseNumber(v[1:]); ok {
					r = append(r, g)
				}
			}
		}
		return r
	}
	taxKey := "taxable_amount"
	if get(taxKey) == "" {
		taxKey = "subtotal"
	}
	tv, xv, gv := variants(get(taxKey)), variants(get("total_tax")), variants(get("grand_total"))
	if len(tv) == 0 || len(xv) == 0 || len(gv) == 0 {
		return
	}
	other, _ := fval(out, "other_charges")
	ro, _ := fval(out, "round_off")
	if math.Abs(tv[0]+xv[0]+other+ro-gv[0]) <= 1.0 {
		return // already adds up
	}
	itemSum, allItems := 0.0, len(out.Items) > 0
	for _, it := range out.Items {
		if it.TaxableValue == nil {
			allItems = false
			break
		}
		itemSum += *it.TaxableValue
	}
	best, bestScore := [3]float64{}, -1.0
	for ti, t := range tv {
		for xi, x := range xv {
			for gi, g := range gv {
				if math.Abs(t+x+other+ro-g) > math.Max(0.05, math.Min(1, g*0.002)) || t <= 0 {
					continue
				}
				score := 0.0
				rate := x / t * 100
				for _, std := range []float64{0, 0.25, 3, 5, 12, 18, 28, 40} {
					if math.Abs(rate-std) < 0.5 {
						score += 2
						break
					}
				}
				if allItems && near(itemSum, t) {
					score += 2
				}
				score -= 0.1 * float64(ti+xi+gi) // prefer values as read
				if score > bestScore {
					best, bestScore = [3]float64{t, x, g}, score
				}
			}
		}
	}
	if bestScore < 0 {
		return
	}
	setV := func(k string, v float64) {
		if f := out.Fields[k]; f != nil && f.Value != fmtNum(v) {
			f.Value = fmtNum(v)
			f.Source += "+glyph_repair"
		}
	}
	setV(taxKey, best[0])
	if taxKey == "taxable_amount" && get("subtotal") != "" {
		if vs := variants(get("subtotal")); len(vs) > 1 && near(vs[1], best[0]) {
			setV("subtotal", best[0])
		}
	}
	setV("total_tax", best[1])
	setV("grand_total", best[2])
	for _, k := range []string{"igst", "cgst", "sgst"} {
		for _, v := range variants(get(k)) {
			if near(v, best[1]) || near(2*v, best[1]) {
				setV(k, v)
				break
			}
		}
	}
	out.Warnings = append(out.Warnings, "totals corrected for an OCR-misread currency symbol; please verify")
}

func isAllCaps(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			letters++
		}
	}
	return letters >= 3
}
