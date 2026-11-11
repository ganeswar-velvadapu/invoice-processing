package parse

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type colKind string

const (
	cSno      colKind = "sno"
	cCode     colKind = "code"
	cDesc     colKind = "desc"
	cHSN      colKind = "hsn"
	cQty      colKind = "qty"
	cUnit     colKind = "unit"
	cRate     colKind = "rate"
	cDisc     colKind = "disc"
	cTaxable  colKind = "taxable"
	cTaxRate  colKind = "taxrate"
	cTaxAmt   colKind = "taxamt"
	cAmount   colKind = "amount"
	cOrdered  colKind = "ordered"
	cReceived colKind = "received"
	cAccepted colKind = "accepted"
	cRejected colKind = "rejected"
	cOther    colKind = "other" // remarks, reasons, notes: never part of the description
)

type kwRule struct {
	kind colKind
	re   *regexp.Regexp
}

// Ordered most specific first; matched spans are consumed so "Taxable Value" is not also "Value".
var headerRules = func() []kwRule {
	r := func(k colKind, p string) kwRule { return kwRule{k, regexp.MustCompile(`(?i)` + p)} }
	return []kwRule{
		r(cTaxAmt, `\b(cgst|sgst|igst|utgst|gst|tax)\s*(amt|amount)\b\.?`),
		r(cTaxRate, `\b(cgst|sgst|igst|utgst|gst|tax)\s*(rate|%)\s*%?|\brate\s*%|\b(gst|tax)\s*\(%\)`),
		r(cTaxable, `\btaxable\s*(value|amt|amount|val)?\b\.?`),
		r(cTaxAmt, `\b(tax|gst)\b(\s*\([^)]{0,8}\))?`),
		r(cOrdered, `\b(qty|quantity)\s*ord(ered)?\b|\bord(ered)?\.?\s*(qty|quantity)\b|\bordered\b|\bord\b\.?`),
		r(cReceived, `\b(qty|quantity)\s*rec(ei|ie)?v?e?d\b|\brec(ei|ie)ved\s*(qty|quantity)?\b|\b(rcvd|recd|rec'd|rcd)\b\.?(\s*(qty|quantity))?`),
		r(cAccepted, `\b(qty|quantity)\s*acc(epted)?\b|\baccepted\s*(qty|quantity)?\b|\b(acc|accpt|acptd)\b\.?(\s*(qty|quantity))?`),
		r(cRejected, `\b(qty|quantity)\s*rej(ected)?\b|\brejected\s*(qty|quantity)?\b|\brej\b\.?(\s*(qty|quantity))?`),
		r(cRate, `\bunit\s*(price|rate|cost)\b|\brate\b|\bprice\b`),
		r(cDisc, `\bdisc(ount)?\.?\s*(%|amt|amount)?`),
		r(cQty, `\b(qty|quantity|qnty|nos)\b\.?`),
		r(cHSN, `\bhsn\s*/?\s*(sac)?(\s*code)?\b|\bsac\b`),
		r(cCode, `\b(item|product|material|mat|part)\s*(code|no\.?|number|#)|\bsku\b|\bcode\b`),
		r(cDesc, `\b(description|particulars|item\s*(name|description)?|product(\s*name)?|goods|material|services?|details)\b`),
		r(cUnit, `\b(uom|unit|uqc|per)\b`),
		r(cAmount, `\b(line\s*)?(total|amount|net\s*amount|value)\b(\s*\(?(rs|inr|₹)\.?\)?)?`),
		r(cSno, `\b(s\.?\s*no|sl\.?\s*no|sr\.?\s*no|line)\b\.?|\b(sl|sr|s)\.?\s*#|^\s*#`),
		r(cOther, `\b(remarks?|reason|notes?|comments?|rejection\s*reason|batch(\s*no)?|serial\s*no)\b`),
	}
}()

type column struct {
	kind       colKind
	start, end int // rune positions
}

type span struct {
	text       string
	start, end int
}

// cells splits a line into text runs separated by 2+ spaces (rune positions).
func cells(line string) []span {
	r := []rune(line)
	var out []span
	i := 0
	for i < len(r) {
		for i < len(r) && r[i] == ' ' {
			i++
		}
		if i >= len(r) {
			break
		}
		s := i
		for i < len(r) {
			if r[i] == ' ' && (i+1 >= len(r) || r[i+1] == ' ') {
				break
			}
			i++
		}
		out = append(out, span{text: string(r[s:i]), start: s, end: i})
	}
	return out
}

// splitTokens breaks a cell into single-space tokens with positions.
func splitTokens(c span) []span {
	var out []span
	r := []rune(c.text)
	i := 0
	for i < len(r) {
		for i < len(r) && r[i] == ' ' {
			i++
		}
		s := i
		for i < len(r) && r[i] != ' ' {
			i++
		}
		if s < i {
			out = append(out, span{text: string(r[s:i]), start: c.start + s, end: c.start + i})
		}
	}
	return out
}

// detectHeader returns columns if the line looks like an items-table header.
func detectHeader(text string) []column { return detectHeaderCells(cells(text)) }

func detectHeaderCells(cs []span) []column {
	var cols []column
	for _, c := range cs {
		used := make([]bool, len([]rune(c.text)))
		type hit struct {
			kind       colKind
			start, end int
		}
		var hits []hit
		for _, rule := range headerRules {
			for _, loc := range rule.re.FindAllStringIndex(c.text, -1) {
				s := len([]rune(c.text[:loc[0]]))
				e := len([]rune(c.text[:loc[1]]))
				free := true
				for k := s; k < e && k < len(used); k++ {
					if used[k] {
						free = false
						break
					}
				}
				if !free || e <= s {
					continue
				}
				for k := s; k < e && k < len(used); k++ {
					used[k] = true
				}
				hits = append(hits, hit{rule.kind, c.start + s, c.start + e})
			}
		}
		if len(hits) == 0 {
			continue
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })
		if len(hits) == 1 {
			cols = append(cols, column{hits[0].kind, c.start, c.end})
			continue
		}
		for i, h := range hits {
			end := c.end
			if i+1 < len(hits) {
				end = hits[i+1].start - 1
			}
			start := h.start
			if i == 0 {
				start = c.start
			}
			cols = append(cols, column{h.kind, start, end})
		}
	}
	kinds := map[colKind]bool{}
	for _, c := range cols {
		kinds[c.kind] = true
	}
	hasText := kinds[cDesc] || kinds[cCode]
	hasNum := kinds[cQty] || kinds[cRate] || kinds[cAmount] || kinds[cOrdered] || kinds[cReceived] || kinds[cAccepted] || kinds[cTaxable]
	if !hasText || !hasNum || len(kinds) < 3 {
		return nil
	}
	return cols
}

// mergeSpans folds a lower header line into the cells of the line above it
// (e.g. "Ord" over "Qty" -> "Ord Qty"). Returns nil if the lower line carries data.
func mergeSpans(upper []span, lowerText string) []span {
	lower := cells(lowerText)
	if len(lower) == 0 {
		return nil
	}
	for _, c := range lower {
		if strings.ContainsAny(c.text, "0123456789") {
			return nil
		}
	}
	out := append([]span{}, upper...)
	for _, b := range lower {
		merged := false
		for k := range out {
			if b.start < out[k].end+2 && b.end > out[k].start-2 {
				out[k].text += " " + b.text
				out[k].start = min(out[k].start, b.start)
				out[k].end = max(out[k].end, b.end)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// mergeHeader is mergeSpans for two raw lines (kept for callers/tests).
func mergeHeader(l1, l2 string) []span { return mergeSpans(cells(l1), l2) }

// headerish: a line with header words and no digits (part of a multi-line header).
func headerish(text string) bool {
	if strings.TrimSpace(text) == "" || strings.ContainsAny(text, "0123456789") {
		return false
	}
	for _, r := range headerRules {
		if r.re.MatchString(text) {
			return true
		}
	}
	return false
}

var (
	stopRe       = regexp.MustCompile(`(?i)^\s*(sub\s*-?\s*totals?|grand\s*totals?|totals?\b|taxable\s*(value|amount)|amount\s*(chargeable\s*)?in\s*words|in\s*words|rupees\b|(c|s|i|ut)gst\b|round(ed)?\s*off|less\s*:|add\s*:|bank\s*details|declaration|terms\s*(&|and)\s*conditions|for\s+[a-z].*\bauthori[sz]ed|authori[sz]ed\s+signatory|net\s*amount|amount\s*payable|total\s*amount|hsn(\s*/\s*sac)?\s.*\btaxable)`)
	pageNoiseRe  = regexp.MustCompile(`(?i)^\s*(page\s*\d+\s*(of\s*\d+)?|continued|contd\.?|\(continued\)|this is a computer generated.*)\s*$`)
	codeInDescRe = regexp.MustCompile(`^\(?([A-Z]{2,}[-/]?[A-Z0-9]*\d[A-Z0-9\-/]*)\)?[\s:\-–]+`)
	snoRe        = regexp.MustCompile(`^\d{1,3}[.)]?$`)
)

// near: equal within rounding. Tolerance scales with the amount (5 paise for small values,
// at most ₹1) so tiny amounts are not "near" everything.
func near(a, b float64) bool {
	return math.Abs(a-b) <= math.Max(0.05, math.Min(1.0, math.Abs(b)*0.005))
}

// parseTable finds the items table and returns items plus the [start,end) line range consumed.
// headerAt returns the table columns for a header detected at line i, folding in multi-line header
// fragments, plus the line where the header block starts and the first data line.
func (d *doc) headerAt(i int) ([]column, int, int) {
	cols := detectHeader(d.lines[i].Text)
	if cols == nil {
		return nil, 0, 0
	}
	start := i
	// a line above joins only if it holds several column words (not a section title such as
	// "ITEM-WISE RECEIPT DETAILS"); up to two header lines below join as well
	cs := cells(d.lines[i].Text)
	if i > 0 && d.lines[i-1].Page == d.lines[i].Page && headerish(d.lines[i-1].Text) && len(cells(d.lines[i-1].Text)) >= 2 {
		if m := mergeSpans(cells(d.lines[i-1].Text), d.lines[i].Text); m != nil {
			cs = m
			start = i - 1
		}
	}
	for n := 0; n < 2 && i+1 < len(d.lines) && d.lines[i+1].Page == d.lines[i].Page && headerish(d.lines[i+1].Text); n++ {
		m := mergeSpans(cs, d.lines[i+1].Text)
		if m == nil {
			break
		}
		cs = m
		i++
	}
	if c2 := detectHeaderCells(cs); c2 != nil && len(c2) >= len(cols) {
		cols = c2
	}
	return cols, start, i + 1
}

// parseTable finds the items table and returns items plus the [start,end) line range consumed.
// Tables continued on later pages (repeated header, possibly shifted columns, often after a
// page-level "TOTALS" row) are read segment by segment and joined.
func (d *doc) parseTable(docType string) ([]Item, int, int) {
	for i := 0; i < len(d.lines); i++ {
		cols, start, from := d.headerAt(i)
		if cols == nil {
			continue
		}
		items, end := d.readRows(cols, from, docType)
		if len(items) == 0 {
			i = from - 1
			continue
		}
		for end < len(d.lines) {
			// continuation: a header within the first lines of a later page
			page := d.lines[end-1].Page
			next := -1
			for j := end; j < len(d.lines) && d.lines[j].Page <= page+1; j++ {
				if d.lines[j].Page == page {
					continue
				}
				pageStart := j
				for j < len(d.lines) && d.lines[j].Page == d.lines[pageStart].Page && j-pageStart < 8 {
					if detectHeader(d.lines[j].Text) != nil {
						next = j
						break
					}
					j++
				}
				break
			}
			if next < 0 {
				break
			}
			c2, _, f2 := d.headerAt(next)
			more, e2 := d.readRows(c2, f2, docType)
			if len(more) == 0 {
				break
			}
			items = append(items, more...)
			end = e2
		}
		for k := range items {
			items[k].LineNumber = k + 1
		}
		return items, start, end
	}
	// No usable header: arithmetic fallback over the whole document.
	items, s, e := d.arithmeticRows(docType)
	return items, s, e
}

func regions(cols []column) [][2]int {
	sort.Slice(cols, func(i, j int) bool { return cols[i].start < cols[j].start })
	out := make([][2]int, len(cols))
	for i, c := range cols {
		lo, hi := 0, 1<<30
		if i > 0 {
			lo = (cols[i-1].end + c.start) / 2
		}
		if i+1 < len(cols) {
			hi = (c.end + cols[i+1].start) / 2
		}
		out[i] = [2]int{lo, hi}
	}
	return out
}

func overlap(a0, a1, b0, b1 int) int {
	return max(0, min(a1, b1)-max(a0, b0))
}

// rowLine is one table line after cell assignment.
type rowLine struct {
	idx      int
	assigned map[colKind][]string
	numeric  int  // number of numeric cells
	text     bool // has description / code text
	keyed    bool // has an item code or serial number: identifies a real row
}

// isAnchor: a real item row has two numbers, or one number plus an item code / serial number.
// A description piece that happens to share its line with a floating quantity is not a row.
func (r rowLine) isAnchor() bool { return r.numeric >= 2 || (r.numeric == 1 && r.keyed) }

// readRows parses table rows in two passes. Pass 1 assigns every line's cells to columns and stops at
// the totals block. Pass 2 builds an item from each numeric row and attaches the rest to the nearest
// row: description pieces of vertically centred multi-line cells (above and below the numbers) and
// floating numeric cells printed slightly off the row (e.g. a quantity nudged up by a warning icon).
func (d *doc) readRows(cols []column, from int, docType string) ([]Item, int) {
	regs := regions(cols)
	var rows []rowLine
	blankRun, textRun, numericSeen, lastRowPage := 0, 0, false, 0
	i := from
	for ; i < len(d.lines); i++ {
		l := d.lines[i]
		t := l.Text
		if strings.TrimSpace(t) == "" {
			blankRun++
			if blankRun > 3 && numericSeen {
				break
			}
			continue
		}
		blankRun = 0
		if pageNoiseRe.MatchString(t) {
			continue
		}
		if detectHeader(t) != nil {
			if numericSeen && l.Page != lastRowPage {
				break // repeated header on a new page: parseTable re-reads it with that page's own columns
			}
			continue
		}
		if stopRe.MatchString(t) {
			break
		}
		if letterCount(t) == 0 && !strings.ContainsAny(t, "0123456789") {
			continue // icons, dashes, rules: noise, not the end of the table
		}
		r := rowLine{idx: i, assigned: map[colKind][]string{}}
		isOCR := l.Conf > 0
		for _, c := range cells(t) {
			c.text = stripSymbols(c.text)
			if c.text == "" {
				continue
			}
			if isOCR {
				c.text = normalizeOCRCell(c.text)
			}
			toks := splitMixedCell(c)
			for _, tk := range toks {
				best, bestOv := -1, 0
				for ci, rg := range regs {
					if ov := overlap(tk.start, tk.end, rg[0], rg[1]); ov > bestOv {
						best, bestOv = ci, ov
					}
				}
				if best < 0 {
					continue
				}
				k := cols[best].kind
				if k == cSno && !IsNumericCell(tk.text) && hasKind(cols, cDesc) {
					k = cDesc // words that drifted under a narrow serial-number column belong to the description
				}
				if k == cOther {
					continue // remarks / reasons are not part of the item
				}
				keepTax := (k == cTaxAmt || k == cTaxRate) && (taxPairRe.MatchString(tk.text) || taxCellRe.MatchString(tk.text))
				if keepTax {
					r.numeric++
				}
				if !keepTax && !IsNumericCell(tk.text) && isNumericKind(k) {
					if _, _, ok := splitQtyUnit(tk.text); !(ok && (k == cQty || k == cOrdered || k == cReceived || k == cAccepted)) {
						if letterCount(tk.text) < 3 {
							continue // OCR junk such as "= 20.2?" in a numeric column
						}
						if !hasKind(cols, cDesc) || k == cUnit {
							continue
						}
						if hasKind(cols, cOther) {
							continue // a remarks column exists: stray text here is a remark, not description
						}
						k = cDesc
					}
				}
				if isNumericKind(k) && IsNumericCell(tk.text) {
					r.numeric++
				}
				if k == cDesc || k == cCode {
					r.text = true
				}
				if k == cCode || k == cSno {
					r.keyed = true
				}
				r.assigned[k] = append(r.assigned[k], tk.text)
			}
		}
		r.assigned = alignNumeric(cols, r.assigned, t)
		isRow := r.isAnchor()
		if isRow {
			numericSeen = true
			textRun = 0
			lastRowPage = l.Page
		} else if r.text {
			textRun++
			// item descriptions often wrap over several lines plus HSN / category lines; only a long
			// run of text with no numbers means we have left the table
			if numericSeen && textRun > 8 {
				break
			}
		}
		rows = append(rows, r)
	}

	// pass 2: anchor rows and attachments
	var anchors []int // indexes into rows
	for k, r := range rows {
		if r.isAnchor() {
			anchors = append(anchors, k)
		}
	}
	if len(anchors) == 0 {
		return nil, i
	}
	descBefore := make(map[int][]string) // anchor -> description pieces above it
	descAfter := make(map[int][]string)  // anchor -> description pieces below it
	isAnchor := map[int]bool{}
	for _, a := range anchors {
		isAnchor[a] = true
	}
	// floating numeric cells: fill the empty column on the nearest row
	for k, r := range rows {
		if isAnchor[k] || r.numeric != 1 {
			continue
		}
		best, bestD := -1, 1<<30
		for _, a := range anchors {
			dd := rows[a].idx - r.idx
			if dd < 0 {
				dd = -dd
			}
			if dd < bestD {
				best, bestD = a, dd
			}
		}
		if best < 0 || bestD > 3 {
			continue
		}
		for kind, vals := range r.assigned {
			if isNumericKind(kind) && len(rows[best].assigned[kind]) == 0 {
				rows[best].assigned[kind] = vals
			}
		}
	}
	// description pieces between anchors. Layout is detected from the first row: text above it means
	// vertically centred cells (each row has as many lines below as above); none means top-aligned
	// (every continuation line belongs to the row above it).
	textRows := func(lo, hi int) []int { // text-only rows strictly between rows lo and hi
		var out []int
		for k := lo + 1; k < hi; k++ {
			if !isAnchor[k] && rows[k].text && rows[k].numeric <= 1 && len(rows[k].assigned[cDesc]) > 0 {
				out = append(out, k)
			}
		}
		return out
	}
	addPiece := func(dst map[int][]string, a, k int) {
		dst[a] = append(dst[a], strings.Join(rows[k].assigned[cDesc], " "))
		if codes := rows[k].assigned[cCode]; len(codes) > 0 && len(rows[a].assigned[cCode]) == 0 {
			rows[a].assigned[cCode] = codes
		}
	}
	lead := textRows(-1, anchors[0])
	if len(lead) > 3 {
		lead = lead[len(lead)-3:] // only lines adjacent to the first row can belong to it
	}
	centred := len(lead) > 0
	for _, k := range lead {
		addPiece(descBefore, anchors[0], k)
	}
	for n, a := range anchors {
		next := len(rows)
		if n+1 < len(anchors) {
			next = anchors[n+1]
		}
		gap := textRows(a, next)
		below := len(gap)
		if centred && n+1 < len(anchors) {
			below = min(len(descBefore[a]), len(gap))
		} else if n+1 < len(anchors) && below > 0 && len(rows[next].assigned[cDesc]) == 0 {
			below-- // the next row has no description on its own line: its cell is centred, the line above is its start
		}
		if n+1 == len(anchors) && below > 3 {
			below = 3
		}
		for i, k := range gap {
			if i < below {
				addPiece(descAfter, a, k)
			} else if n+1 < len(anchors) {
				addPiece(descBefore, anchors[n+1], k)
			}
		}
	}

	var items []Item
	for _, a := range anchors {
		r := rows[a]
		l := d.lines[r.idx]
		asg := r.assigned
		desc := append(append(append([]string{}, descBefore[a]...), asg[cDesc]...), descAfter[a]...)
		asg[cDesc] = desc
		it := buildItem(asg, docType)
		it.Page = l.Page
		it.Raw = l.Text
		it.Source = "table"
		if l.Conf > 0 && docType != "GRN" {
			repairCurrencyGlyph(&it, asg)
		}
		finishItem(&it, docType, l.Text)
		it.Confidence = round4(ocrFactor(l, it.Confidence))
		if it.Quantity == nil && it.TaxableValue == nil && it.LineTotal == nil && it.QuantityReceived == nil {
			continue
		}
		items = append(items, it)
	}
	for k := range items {
		items[k].LineNumber = k + 1
	}
	return items, i
}

func hasKind(cols []column, k colKind) bool {
	for _, c := range cols {
		if c.kind == k {
			return true
		}
	}
	return false
}

func isNumericKind(k colKind) bool {
	switch k {
	case cQty, cRate, cDisc, cTaxable, cTaxRate, cTaxAmt, cAmount, cOrdered, cReceived, cAccepted, cRejected:
		return true
	}
	return false
}

func num(vals []string) *float64 {
	var sum float64
	found := false
	for _, v := range vals {
		if f, ok := ParseNumber(v); ok {
			sum += f
			found = true
		} else if f, _, ok := splitQtyUnit(v); ok {
			sum += f
			found = true
		}
	}
	if !found {
		return nil
	}
	return &sum
}

func firstNum(vals []string) *float64 {
	for _, v := range vals {
		if f, ok := ParseNumber(v); ok {
			return &f
		}
		if f, _, ok := splitQtyUnit(v); ok {
			return &f
		}
	}
	return nil
}

func buildItem(a map[colKind][]string, docType string) Item {
	it := Item{}
	it.ItemCode = strings.Join(a[cCode], " ")
	it.Description = strings.Join(a[cDesc], " ")
	it.HSNSAC = strings.Join(a[cHSN], "")
	// "73181500 KG": a unit word glued to the HSN code
	if f := strings.Fields(strings.Join(a[cHSN], " ")); len(f) == 2 && regexp.MustCompile(`^\d{4,8}$`).MatchString(f[0]) && letterCount(f[1]) == len(f[1]) {
		it.HSNSAC = f[0]
		if len(a[cUnit]) == 0 {
			a[cUnit] = []string{f[1]}
		}
	}
	it.Unit = strings.Join(a[cUnit], " ")
	if f := strings.Fields(strings.Join(a[cHSN], " ")); it.Unit == "" && len(f) == 2 && letterCount(f[1]) == len(f[1]) {
		it.Unit = f[1]
	}
	for _, v := range append(append(append([]string{}, a[cQty]...), a[cOrdered]...), a[cReceived]...) {
		if _, u, ok := splitQtyUnit(v); ok && it.Unit == "" {
			it.Unit = u
		}
	}
	it.UnitPrice = firstNum(a[cRate])
	it.TaxableValue = firstNum(a[cTaxable])
	it.TaxRate = num(a[cTaxRate]) // CGST% + SGST% columns add up to the GST rate
	it.TaxAmount = num(a[cTaxAmt])
	// combined cells like "IGST 18% = ₹0.25" (or just "IGST 18% = <unreadable>")
	for _, v := range append(append([]string{}, a[cTaxAmt]...), a[cTaxRate]...) {
		if m := taxPairRe.FindStringSubmatch(v); m == nil {
			if r := taxRateOnlyRe.FindStringSubmatch(v); r != nil && taxCellRe.MatchString(v) {
				rv, _ := ParseNumber(r[1])
				it.TaxRate = &rv
				if it.TaxAmount != nil && (*it.TaxAmount == rv || !IsNumericCell(v)) {
					it.TaxAmount = nil
				}
			}
			continue
		}
		if m := taxPairRe.FindStringSubmatch(v); m != nil {
			r, _ := ParseNumber(m[1])
			amt, _ := ParseNumber(m[2])
			if it.TaxRate == nil || *it.TaxRate == r || *it.TaxRate > 100 {
				it.TaxRate = &r
			}
			if it.TaxAmount == nil || *it.TaxAmount == r {
				it.TaxAmount = &amt
			}
		}
	}
	it.LineTotal = firstNum(a[cAmount])
	if len(a[cDisc]) > 0 {
		raw := strings.Join(a[cDisc], "")
		if f := firstNum(a[cDisc]); f != nil {
			if strings.Contains(raw, "%") {
				it.Discount = nil
				pct := *f
				it.discountPct = &pct
			} else {
				it.Discount = f
			}
		}
	}
	switch docType {
	case "GRN":
		it.QuantityReceived = firstNum(a[cReceived])
		it.QuantityAccepted = firstNum(a[cAccepted])
		it.QuantityRejected = firstNum(a[cRejected])
		it.Quantity = firstNum(a[cOrdered])
		if it.QuantityReceived == nil {
			it.QuantityReceived = firstNum(a[cQty])
		}
	case "PO":
		it.Quantity = firstNum(a[cOrdered])
		if it.Quantity == nil {
			it.Quantity = firstNum(a[cQty])
		}
	default:
		it.Quantity = firstNum(a[cQty])
		if it.Quantity == nil {
			it.Quantity = firstNum(a[cOrdered])
		}
	}
	// serial-number column absorbed into description
	if f := strings.Fields(it.Description); len(f) > 1 && snoRe.MatchString(f[0]) && len(a[cSno]) == 0 {
		it.Description = strings.Join(f[1:], " ")
	}
	return it
}

// finishItem derives missing values, cross-checks arithmetic and sets confidence.
var hsnInDescRe = regexp.MustCompile(`(?i)\s*\bHSN\s*/?\s*(?:SAC)?\s*(?:code)?\s*[:\-]?\s*(\d{4,8})\b.*$`)

func finishItem(it *Item, docType, raw string) {
	// "Seals - Lip HSN/SAC: 40169390 | Rubber And Articles Thereof": HSN into its field, category text dropped
	if m := hsnInDescRe.FindStringSubmatchIndex(it.Description); m != nil && m[0] > 0 {
		if it.HSNSAC == "" {
			it.HSNSAC = it.Description[m[2]:m[3]]
		}
		it.Description = strings.TrimSpace(it.Description[:m[0]])
	}
	if it.ItemCode == "" {
		if m := codeInDescRe.FindStringSubmatch(it.Description); m != nil {
			it.ItemCode = m[1]
			it.Description = strings.TrimSpace(it.Description[len(m[0]):])
		}
	}
	it.Description = strings.TrimSpace(it.Description)
	if docType == "GRN" {
		if it.QuantityReceived != nil && it.QuantityAccepted == nil {
			acc := *it.QuantityReceived
			if it.QuantityRejected != nil {
				acc -= *it.QuantityRejected
			}
			it.QuantityAccepted = &acc
		}
		if it.QuantityRejected == nil && it.QuantityReceived != nil && it.QuantityAccepted != nil {
			rej := *it.QuantityReceived - *it.QuantityAccepted
			it.QuantityRejected = &rej
		}
		it.Confidence = 0.6
		if it.QuantityReceived != nil && it.QuantityAccepted != nil && it.QuantityRejected != nil &&
			near(*it.QuantityReceived, *it.QuantityAccepted+*it.QuantityRejected) {
			it.Confidence = 0.9
		}
		return
	}
	q, p := it.Quantity, it.UnitPrice
	if it.discountPct != nil && q != nil && p != nil {
		d := *q * *p * *it.discountPct / 100
		it.Discount = &d
	}
	disc := 0.0
	if it.Discount != nil {
		disc = *it.Discount
	}
	// Single "Amount" column: decide whether it is pre-tax (taxable) or tax-inclusive.
	if it.TaxableValue == nil && it.LineTotal != nil && q != nil && p != nil {
		gross := *q**p - disc
		switch {
		case near(gross, *it.LineTotal):
			v := *it.LineTotal
			it.TaxableValue = &v
		case it.TaxRate != nil && near(gross*(1+*it.TaxRate/100), *it.LineTotal):
			v := gross
			it.TaxableValue = &v
		}
	}
	// derive unit price / quantity when one of them is missing
	if p == nil && q != nil && *q != 0 && it.TaxableValue != nil {
		v := (*it.TaxableValue + disc) / *q
		it.UnitPrice = &v
		p = it.UnitPrice
	}
	if it.TaxAmount == nil && it.TaxRate != nil && it.TaxableValue != nil {
		v := math.Round(*it.TaxableValue**it.TaxRate) / 100
		it.TaxAmount = &v
	}
	if it.TaxRate == nil && it.TaxAmount != nil && it.TaxableValue != nil && *it.TaxableValue != 0 {
		// only accept a derived rate that is a real GST slab; anything else is misread data
		v := math.Round(*it.TaxAmount / *it.TaxableValue * 10000) / 100
		for _, std := range []float64{0, 0.25, 3, 5, 12, 18, 28, 40} {
			if math.Abs(v-std) < 0.5 {
				r := std
				it.TaxRate = &r
				break
			}
		}
	}
	if it.LineTotal == nil && it.TaxableValue != nil {
		v := *it.TaxableValue
		if it.TaxAmount != nil {
			v += *it.TaxAmount
		}
		v = math.Round(v*100) / 100
		it.LineTotal = &v
	}
	if it.TaxableValue == nil && q != nil && p != nil {
		v := math.Round((*q**p-disc)*100) / 100
		it.TaxableValue = &v
	}
	it.Confidence = 0.65
	if q != nil && p != nil && it.TaxableValue != nil {
		if near(*q**p-disc, *it.TaxableValue) {
			it.Confidence = 0.92
		} else {
			it.Confidence = 0.45
			// try an arithmetic re-read of the raw line
			if alt := arithmeticItem(raw); alt != nil {
				code, desc := it.ItemCode, it.Description
				*it = *alt
				if it.ItemCode == "" {
					it.ItemCode = code
				}
				if desc != "" {
					it.Description = desc
				}
				it.Confidence = 0.75
			}
		}
	}
	if docType == "PO" && it.Quantity != nil && it.UnitPrice != nil && it.Confidence < 0.6 {
		it.Confidence = 0.6
	}
}

// arithmeticItem finds qty × price ≈ amount among the numeric tokens of a line.
func arithmeticItem(line string) *Item {
	type tok struct {
		v     float64
		s     string
		start int
		pct   bool
	}
	var toks []tok
	for _, c := range cells(line) {
		for _, t := range splitTokens(c) {
			if IsNumericCell(t.text) {
				if f, ok := ParseNumber(t.text); ok {
					toks = append(toks, tok{f, t.text, t.start, strings.HasSuffix(t.text, "%")})
				}
			}
		}
	}
	if len(toks) < 3 {
		return nil
	}
	for k := len(toks) - 1; k >= 2; k-- {
		for i := 0; i < k; i++ {
			for j := i + 1; j < k; j++ {
				q, p, a := toks[i], toks[j], toks[k]
				if q.pct || p.pct || a.pct || q.v <= 0 || p.v <= 0 || a.v <= 0 || q.v > 1e6 {
					continue
				}
				if math.Abs(q.v*p.v-a.v) <= math.Max(0.01, a.v*0.002) {
					it := &Item{Source: "arithmetic", Raw: line}
					qv, pv, av := q.v, p.v, a.v
					it.Quantity, it.UnitPrice, it.TaxableValue = &qv, &pv, &av
					r := []rune(line)
					desc := strings.TrimSpace(string(r[:min(q.start, len(r))]))
					// drop leading serial and trailing HSN code from description
					f := strings.Fields(desc)
					if len(f) > 0 && snoRe.MatchString(f[0]) {
						f = f[1:]
					}
					if len(f) > 0 && regexp.MustCompile(`^\d{4,8}$`).MatchString(f[len(f)-1]) {
						it.HSNSAC = f[len(f)-1]
						f = f[:len(f)-1]
					}
					it.Description = strings.Join(f, " ")
					// unit word right after quantity
					after := strings.Fields(string(r[min(q.start, len(r)):]))
					if len(after) > 1 && regexp.MustCompile(`^[A-Za-z]{1,6}\.?$`).MatchString(after[1]) {
						it.Unit = strings.TrimSuffix(after[1], ".")
					}
					for _, t := range toks[k+1:] {
						if t.pct && t.v <= 28 {
							v := t.v
							it.TaxRate = &v
						}
					}
					for _, t := range toks[j+1 : k] {
						if t.pct && t.v <= 28 {
							v := t.v
							it.TaxRate = &v
						}
					}
					if len(toks) > k+1 && !toks[len(toks)-1].pct && toks[len(toks)-1].v > av {
						lt := toks[len(toks)-1].v
						it.LineTotal = &lt
					}
					if code := codeInDescRe.FindStringSubmatch(it.Description); code != nil {
						it.ItemCode = code[1]
						it.Description = strings.TrimSpace(it.Description[len(code[0]):])
					}
					return it
				}
			}
		}
	}
	return nil
}

func (d *doc) arithmeticRows(docType string) ([]Item, int, int) {
	var items []Item
	first, last := -1, -1
	for i, l := range d.lines {
		if stopRe.MatchString(l.Text) && len(items) > 0 {
			break
		}
		it := arithmeticItem(l.Text)
		if it == nil {
			continue
		}
		finishItem(it, docType, l.Text)
		it.Confidence = round4(ocrFactor(l, math.Min(it.Confidence, 0.7)))
		it.Page = l.Page
		it.LineNumber = len(items) + 1
		items = append(items, *it)
		if first < 0 {
			first = i
		}
		last = i + 1
	}
	return items, max(first, 0), max(last, 0)
}

// OCR reads the rupee sign as %, =, T, F, Z, ? ... directly in front of an amount ("T0.01", "=40,936.06").
var (
	glyphNumRe    = regexp.MustCompile(`^[%=FfTtZzR₹?{\[]\s?([0-9oO][0-9oO,]*(?:\.[0-9oO]{1,3})?)$`)
	digitishRe    = regexp.MustCompile(`^[0-9oO][0-9oO,]*(?:\.[0-9oO]{1,3})?$`)
	taxCellRe     = regexp.MustCompile(`(?i)\b(c|s|i|ut)?gst\b|\d\s*%`)
	taxRateOnlyRe = regexp.MustCompile(`(\d{1,2}(?:\.\d{1,2})?)\s*%`)
	taxPairRe     = regexp.MustCompile(`(\d{1,2}(?:\.\d{1,2})?)\s*%\s*=?\s*\S?\s*([\d,]+\.\d{1,2})`)
	glyphDigits   = "2757"
)

func normalizeOCRCell(s string) string {
	t := strings.TrimSpace(s)
	if m := glyphNumRe.FindStringSubmatch(t); m != nil {
		t = m[1]
	} else if !digitishRe.MatchString(t) {
		return s
	}
	if !strings.ContainsAny(t, "0123456789") {
		return s
	}
	return strings.NewReplacer("o", "0", "O", "0").Replace(t)
}

// repairCurrencyGlyph handles a rupee sign OCR'd as a digit ("₹0.01" -> "20.01" / "70.01"):
// when quantity × price does not equal an amount on the row, try dropping a leading 2/7/5 from the
// price and from each amount cell (pre-tax or tax-inclusive) and keep the combination whose
// arithmetic is exact.
func repairCurrencyGlyph(it *Item, a map[colKind][]string) {
	if len(a[cRate]) == 0 {
		return
	}
	type amt struct {
		v       float64
		taxable bool // came from a "Taxable value" column
	}
	variants := func(raw string) []float64 {
		raw = strings.TrimSpace(raw)
		var out []float64
		if f, ok := ParseNumber(raw); ok {
			out = append(out, f)
		}
		if len(raw) >= 4 && strings.ContainsRune(glyphDigits, rune(raw[0])) {
			if f, ok := ParseNumber(raw[1:]); ok {
				out = append(out, f)
			}
		}
		return out
	}
	var amts []amt
	for _, raw := range a[cTaxable] {
		for _, v := range variants(raw) {
			amts = append(amts, amt{v, true})
		}
	}
	for _, raw := range a[cAmount] {
		for _, v := range variants(raw) {
			amts = append(amts, amt{v, false})
		}
	}
	ps := variants(a[cRate][0])
	if len(amts) == 0 || len(ps) == 0 {
		return
	}
	disc := 0.0
	if it.Discount != nil {
		disc = *it.Discount
	}
	rate := -1.0
	if it.TaxRate != nil {
		rate = *it.TaxRate
	}
	consistent := func(q, p float64) (taxable float64, total *float64, ok bool) {
		base := q*p - disc
		for _, m := range amts {
			if near(base, m.v) {
				return m.v, nil, true
			}
		}
		if rate >= 0 && !it.hasTaxable(a) {
			for _, m := range amts {
				if !m.taxable && near(base*(1+rate/100), m.v) {
					t := m.v
					return base, &t, true
				}
			}
		}
		return 0, nil, false
	}
	apply := func(q, p, taxable float64, total *float64) {
		taxable = math.Round(taxable*100) / 100
		it.Quantity, it.UnitPrice, it.TaxableValue = &q, &p, &taxable
		it.LineTotal = total
		if total == nil {
			it.LineTotal = nil // re-derived by finishItem
		}
	}
	if it.Quantity != nil {
		q := *it.Quantity
		for _, p := range ps { // price as read first, then without a leading glyph digit
			if p <= 0 {
				continue
			}
			if tx, tot, ok := consistent(q, p); ok {
				apply(q, p, tx, tot)
				return
			}
		}
	}
	// quantity itself unreadable ("10t"): accept a whole-number quantity implied by price and amount
	for _, p := range ps {
		for _, m := range amts {
			if p <= 0 || m.v <= 0 {
				continue
			}
			q := (m.v + disc) / p
			if r := math.Round(q); r >= 1 && math.Abs(q-r) < 0.001 && (it.Quantity == nil || closeDigits(*it.Quantity, r)) {
				apply(r, p, m.v, nil)
				return
			}
		}
	}
}

func (it *Item) hasTaxable(a map[colKind][]string) bool { return len(a[cTaxable]) > 0 }

// closeDigits: the OCR'd quantity shares its leading digits with the implied one (107 vs "10t" -> 10).
func closeDigits(ocr, implied float64) bool {
	a, b := fmtNum(ocr), fmtNum(implied)
	return len(a) >= 2 && strings.HasPrefix(b, a[:2])
}

func letterCount(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			n++
		}
	}
	return n
}

var (
	serialRe   = regexp.MustCompile(`^\d{1,3}[.)]?$`)
	codeNumRe  = regexp.MustCompile(`^\d{4,10}$`)
	unitWordRe = regexp.MustCompile(`^[A-Za-z]{2,6}\.?$`)
)

// splitMixedCell splits a cell that pdftotext merged across columns, only in unambiguous shapes:
// all numbers ("10 500.00"), a leading serial number before text ("4 Belts - Round Belt"),
// or a numeric code followed by a unit word ("40103911 PCS"). Text with numbers inside
// ("Ball Bearing 6205") stays whole.
func splitMixedCell(c span) []span {
	parts := splitTokens(c)
	if len(parts) < 2 {
		return []span{c}
	}
	allNum := true
	for _, p := range parts {
		if !IsNumericCell(p.text) {
			allNum = false
			break
		}
	}
	switch {
	case allNum:
		return parts
	case serialRe.MatchString(parts[0].text) && letterCount(parts[1].text) > 0:
		rest := span{text: strings.TrimSpace(string([]rune(c.text)[parts[1].start-c.start:])), start: parts[1].start, end: c.end}
		return []span{parts[0], rest}
	case len(parts) == 2 && codeNumRe.MatchString(parts[0].text) && unitWordRe.MatchString(parts[1].text):
		return parts
	}
	return []span{c}
}

func regionOf(regs [][2]int, x int) int {
	for i, rg := range regs {
		if x >= rg[0] && x < rg[1] {
			return i
		}
	}
	return -1
}

// alignNumeric handles right-aligned numbers drifting under the next column header (compact headers
// such as "UOM Ordered Received Accepted Rejected"): when a row has exactly one plain number per numeric
// column, numbers are assigned to those columns in left-to-right order instead of by position.
func alignNumeric(cols []column, a map[colKind][]string, line string) map[colKind][]string {
	var numCols []column
	for _, c := range cols {
		if isNumericKind(c.kind) {
			numCols = append(numCols, c)
		}
	}
	type tok struct {
		text  string
		start int
	}
	for _, c := range numCols {
		for _, v := range a[c.kind] {
			if !IsNumericCell(v) {
				return a // tax cells like "IGST 18% = 0.25": keep positional assignment
			}
		}
	}
	// recover the left-to-right order of the numbers from the line itself
	var toks []tok
	for _, c := range cells(line) {
		for _, t := range splitTokens(c) {
			if IsNumericCell(t.text) {
				toks = append(toks, tok{t.text, t.start})
			}
		}
	}
	// keep only numbers that were assigned to numeric columns (drop serials, HSN codes)
	var nums []tok
	pool := map[string]int{}
	for _, c := range numCols {
		for _, v := range a[c.kind] {
			pool[v]++
		}
	}
	for _, t := range toks {
		if pool[t.text] > 0 {
			pool[t.text]--
			nums = append(nums, t)
		}
	}
	distinct := map[colKind]bool{}
	for _, c := range numCols {
		distinct[c.kind] = true
	}
	if len(nums) != len(numCols) || len(distinct) != len(numCols) {
		return a
	}
	sort.Slice(numCols, func(i, j int) bool { return numCols[i].start < numCols[j].start })
	out := map[colKind][]string{}
	for k, v := range a {
		if !isNumericKind(k) {
			out[k] = v
		}
	}
	for i, t := range nums {
		out[numCols[i].kind] = append(out[numCols[i].kind], t.text)
	}
	return out
}

// stripSymbols removes leading/trailing tokens with no letters or digits ("58.09 ⚠", "— 0.00"),
// keeping symbols inside text such as the dash in "Training - Installation".
func stripSymbols(t string) string {
	f := strings.Fields(t)
	isSym := func(w string) bool {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return false
			}
		}
		return true
	}
	for len(f) > 0 && isSym(f[0]) {
		f = f[1:]
	}
	for len(f) > 0 && isSym(f[len(f)-1]) {
		f = f[:len(f)-1]
	}
	if len(f) == 0 {
		return ""
	}
	// keep the original spacing of the remaining text
	start := strings.Index(t, f[0])
	end := strings.LastIndex(t, f[len(f)-1]) + len(f[len(f)-1])
	return t[start:end]
}
