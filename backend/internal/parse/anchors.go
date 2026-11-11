package parse

import (
	"math"
	"regexp"
	"strings"

	"invoiceproc/internal/classify"
	"invoiceproc/internal/extract"
)

var (
	docNumRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9\-/._]{0,39}`)
	gapRe    = regexp.MustCompile(`\s{2,}`)
	sepRe    = regexp.MustCompile(`^[\s:#.\-–=|]*`)
)

// ocrFactor scales a confidence by the OCR word confidence of the source line.
func ocrFactor(l extract.Line, conf float64) float64 {
	if l.Conf <= 0 {
		return conf
	}
	f := math.Max(0.3, math.Min(1, l.Conf/100))
	return conf * (0.55 + 0.45*f)
}

type match struct {
	value string
	line  int
	conf  float64
}

// valueFn extracts a value from the start of s; returns "" if none.
type valueFn func(s string) string

func docNumberValue(s string) string {
	s = strings.TrimSpace(s)
	seg := gapRe.Split(s, 2)[0]
	f := strings.Fields(seg)
	if len(f) == 0 {
		return ""
	}
	v := docNumRe.FindString(f[0])
	v = strings.TrimRight(v, ".-/_")
	if v == "" || !strings.ContainsAny(v, "0123456789") || len(v) < 2 {
		return ""
	}
	return v
}

func dateValue(s string) string {
	seg := strings.TrimSpace(s)
	if len(seg) > 30 {
		seg = seg[:30]
	}
	loc := DateRe.FindStringIndex(seg)
	if loc == nil || loc[0] > 3 {
		return ""
	}
	if t, ok := ParseDate(seg[loc[0]:loc[1]]); ok {
		return t.Format("2006-01-02")
	}
	return ""
}

func amountValue(s string) string {
	s = strings.TrimSpace(s)
	loc := numberRe.FindStringIndex(s)
	if loc == nil || loc[0] > 6 {
		return ""
	}
	if f, ok := ParseNumber(s[loc[0]:loc[1]]); ok {
		return fmtNum(f)
	}
	return ""
}

// lastAmount returns the last number on the line (totals rows usually end with the amount).
func lastAmount(s string) string {
	all := numberRe.FindAllString(s, -1)
	for i := len(all) - 1; i >= 0; i-- {
		tok := strings.TrimSpace(all[i])
		if f, ok := ParseNumber(tok); ok {
			return fmtNum(f)
		}
	}
	return ""
}

// signedLastAmount is lastAmount that keeps a leading minus ("Round Off  -0.40"); a hyphen
// glued to a preceding digit or letter is a separator, not a sign.
func signedLastAmount(s string) string {
	locs := numberRe.FindAllStringIndex(s, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		tok := strings.TrimSpace(s[locs[i][0]:locs[i][1]])
		f, ok := ParseNumber(tok)
		if !ok {
			continue
		}
		start := locs[i][0]
		for start < len(s) && s[start] == ' ' {
			start++
		}
		if !strings.HasPrefix(tok, "-") && start > 0 && s[start-1] == '-' && (start == 1 || strings.ContainsRune(" \t:(₹", rune(s[start-2]))) {
			f = -f
		}
		return fmtNum(f)
	}
	return ""
}

// nameValue reads a name: OCR puts 2-3 spaces between words of one name, so only 4+ spaces end it.
func nameValue(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimSpace(wideGapRe.Split(s, 2)[0])
}

var wideGapRe = regexp.MustCompile(`\s{4,}`)

func textValue(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimSpace(gapRe.Split(s, 2)[0])
}

// findLabeled searches for label then value: on the same line after the label, otherwise
// below the label in roughly the same column (stacked "label over value" layouts).
// skip(lineIdx) can exclude lines (e.g. inside the items table).
func (d *doc) findLabeled(labels []*regexp.Regexp, val valueFn, from, to int, skip func(int) bool) *match {
	if to <= 0 || to > len(d.lines) {
		to = len(d.lines)
	}
	for _, re := range labels {
		for i := from; i < to; i++ {
			if skip != nil && skip(i) {
				continue
			}
			text := d.lines[i].Text
			for _, loc := range re.FindAllStringIndex(text, -1) {
				rest := text[loc[1]:]
				rest = rest[len(sepRe.FindString(rest)):]
				if v := val(rest); v != "" {
					return &match{value: v, line: i, conf: ocrFactor(d.lines[i], 0.92)}
				}
				// stacked layout: look at next two lines near the label column
				for j := i + 1; j <= i+2 && j < len(d.lines); j++ {
					if d.lines[j].Page != d.lines[i].Page {
						break
					}
					seg := fromColumn(d.lines[j].Text, len([]rune(text[:loc[0]])))
					if seg == "" {
						continue
					}
					if v := val(seg); v != "" {
						return &match{value: v, line: j, conf: ocrFactor(d.lines[j], 0.8)}
					}
				}
			}
		}
	}
	return nil
}

func (d *doc) field(m *match, source string) *Field {
	if m == nil {
		return nil
	}
	l := d.lines[m.line]
	return &Field{Value: m.value, Confidence: round4(m.conf), Page: l.Page, BBox: l.BBox, Source: source}
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }

func res(ps ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(ps))
	for i, p := range ps {
		out[i] = regexp.MustCompile(`(?i)` + p)
	}
	return out
}

// findFuzzy is the OCR-tolerant fallback for findLabeled: label phrases are matched with an
// edit-distance tolerance ("Tnvoice Number", "Bill Dare") and the value is read after the
// label on the same line, or below it in the same column.
func (d *doc) findFuzzy(phrases []string, val valueFn, skip func(int) bool) *match {
	for _, ph := range phrases {
		for i, l := range d.lines {
			if skip != nil && skip(i) {
				continue
			}
			words := classify.Words(l.Text)
			wi, wj, ok := classify.FuzzyFind(words, ph, 0.22)
			if !ok {
				continue
			}
			r := []rune(l.Text)
			rest := string(r[words[wj-1].End:])
			rest = rest[len(sepRe.FindString(rest)):]
			// the label may continue with a stray OCR'd word ("Order No.:" -> "Order We.:"), skip one short token
			if v := val(rest); v != "" {
				return &match{value: v, line: i, conf: ocrFactor(l, 0.8)}
			}
			if f := strings.Fields(rest); len(f) > 1 && len([]rune(f[0])) <= 4 {
				if v := val(strings.TrimLeft(strings.Join(f[1:], " "), ":-. ")); v != "" {
					return &match{value: v, line: i, conf: ocrFactor(l, 0.7)}
				}
			}
			col := words[wi].Start
			for j := i + 1; j <= i+2 && j < len(d.lines); j++ {
				if d.lines[j].Page != l.Page {
					break
				}
				seg := fromColumn(d.lines[j].Text, col)
				if seg == "" {
					continue
				}
				if v := val(seg); v != "" {
					return &match{value: v, line: j, conf: ocrFactor(d.lines[j], 0.72)}
				}
			}
		}
	}
	return nil
}

// tokenFallback finds unlabelled document numbers by their shape (e.g. "GRN/2526/000005").
func (d *doc) tokenFallback(re *regexp.Regexp, skip func(int) bool) *Field {
	for i, l := range d.lines {
		if skip != nil && skip(i) {
			continue
		}
		if m := re.FindString(l.Text); m != "" {
			conf := 0.65
			if l.Page == 1 && i < 6 {
				conf = 0.88 // the document's own number printed in the header area
			}
			return &Field{Value: strings.TrimRight(m, "/-"), Confidence: round4(ocrFactor(l, conf)), Page: l.Page, BBox: l.BBox, Source: "pattern"}
		}
	}
	return nil
}

// fromColumn returns the text of a line from roughly the given column (a value printed under its
// label). If that column falls inside a word, it starts at the beginning of the word instead, so a
// value that sits a little left of its label ("GST-2024-00000" under "Invoice Ref.") is not cut.
func fromColumn(line string, col int) string {
	r := []rune(line)
	start := max(0, col-4)
	if start >= len(r) {
		return ""
	}
	for start > 0 && r[start] != ' ' && r[start-1] != ' ' {
		start--
	}
	return strings.TrimLeft(string(r[start:]), " ")
}
