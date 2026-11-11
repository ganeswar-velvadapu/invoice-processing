// Package classify assigns a document type from its text.
//
// The decisive evidence is the document's own title (top of page 1): "PURCHASE ORDER",
// "TAX INVOICE", "E-WAY BILL", ... Mentions of *other* documents ("Invoice No.", "PO Ref.",
// "Against PO") are references, so they only add small, capped body weights. All phrase
// matching tolerates OCR errors (fuzzy, edit-distance based), because scanned and
// handwriting-style documents rarely OCR cleanly.
package classify

import (
	"fmt"
	"sort"
	"strings"
)

type Result struct {
	DocumentType string             `json:"document_type"`
	Confidence   float64            `json:"confidence"`
	Scores       map[string]float64 `json:"scores"`
	Evidence     []string           `json:"evidence"`
}

type titleRule struct {
	docType string
	phrase  string
	weight  float64
}

// Document titles. Longer, more specific phrases first.
var titles = []titleRule{
	{"EWAY_BILL", "e way bill", 10}, {"EWAY_BILL", "eway bill", 10}, {"EWAY_BILL", "lorry receipt", 9},
	{"EWAY_BILL", "consignment note", 8}, {"EWAY_BILL", "waybill", 8},
	{"MRN", "material return note", 10}, {"MRN", "material return", 9}, {"MRN", "rejection note", 9},
	{"MRN", "return note", 8}, {"MRN", "debit note", 8},
	{"GRN", "goods receipt note", 10}, {"GRN", "goods received note", 10}, {"GRN", "goods receipt", 9},
	{"GRN", "material receipt note", 10}, {"GRN", "receipt note", 8}, {"GRN", "inward note", 8},
	{"PO", "purchase order", 10}, {"PO", "work order", 7},
	{"INVOICE", "tax invoice", 10}, {"INVOICE", "bill of supply", 10}, {"INVOICE", "commercial invoice", 10},
	{"INVOICE", "proforma invoice", 6}, {"INVOICE", "invoice", 8},
}

// Words that, right after a title phrase, show it is a reference label ("Invoice No.", "PO Date").
var refFollowers = map[string]bool{"no": true, "number": true, "num": true, "ref": true, "reference": true,
	"date": true, "dated": true, "details": true, "value": true, "id": true, "amount": true, "total": true,
	"copy": false, "nos": true, "dt": true, "doc": true, "document": true, "type": true}

// Words right before a title phrase that make it a reference ("against purchase order").
var refLeaders = map[string]bool{"against": true, "your": true, "our": true, "per": true, "with": true, "ref": true, "of": true}

type bodyRule struct {
	docType string
	phrase  string
	weight  float64
}

// Body cues: structure typical of each document. Capped per type (bodyCap).
var body = []bodyRule{
	{"EWAY_BILL", "vehicle no", 2}, {"EWAY_BILL", "transporter", 2}, {"EWAY_BILL", "lr no", 1.5}, {"EWAY_BILL", "valid till", 2},
	{"EWAY_BILL", "ewb", 1.5}, {"EWAY_BILL", "mode of transport", 1.5}, {"EWAY_BILL", "distance", 1},
	{"EWAY_BILL", "part a", 2.5}, {"EWAY_BILL", "part b", 2.5}, {"EWAY_BILL", "sub type", 2}, {"EWAY_BILL", "consignor", 1.5},
	{"EWAY_BILL", "document details", 1}, {"EWAY_BILL", "ship to gstin", 1},
	{"GRN", "received", 1}, {"GRN", "accepted", 1.5}, {"GRN", "rejected", 1}, {"GRN", "rcvd", 1.5}, {"GRN", "grn", 2},
	{"GRN", "receiving location", 2}, {"GRN", "store", 0.5}, {"GRN", "inspected", 1},
	{"MRN", "return reason", 2}, {"MRN", "returned", 1.5}, {"MRN", "qty returned", 2}, {"MRN", "debit", 1},
	{"PO", "order date", 1.5}, {"PO", "delivery date", 1.5}, {"PO", "ship to", 1}, {"PO", "consign to", 1},
	{"PO", "delivery address", 1}, {"PO", "requisition", 1.5}, {"PO", "terms and conditions", 0.5},
	{"INVOICE", "taxable value", 1}, {"INVOICE", "amount payable", 2}, {"INVOICE", "grand total", 1.5},
	{"INVOICE", "amount in words", 1}, {"INVOICE", "bill to", 1}, {"INVOICE", "due date", 1}, {"INVOICE", "place of supply", 1},
	{"INVOICE", "irn", 1.5}, {"INVOICE", "sold by", 1},
}

const (
	titleZone  = 12   // first non-empty lines of page 1 treated as the title area
	bodyCap    = 5.0  // max body score per type
	fuzzyRatio = 0.20 // up to ~1 edit per 5 characters
)

// Classify scores the lines of a document (page breaks are not needed; the first lines are the top of page 1).
func Classify(lines []string) Result {
	scores := map[string]float64{}
	bodyScores := map[string]float64{}
	titleHit := map[string]float64{}
	var evidence []string
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n++
		words := Words(l)
		if len(words) == 0 {
			continue
		}
		inTitle := n <= titleZone
		matchedSpans := [][2]int{}
		for _, t := range titles {
			for off := 0; off < len(words); {
				i, j, ok := FuzzyFind(words[off:], t.phrase, fuzzyRatio)
				if !ok {
					break
				}
				i, j = i+off, j+off
				off = j
				if overlaps(matchedSpans, i, j) {
					continue
				}
				matchedSpans = append(matchedSpans, [2]int{i, j})
				// "Invoice: X", "PO # X", "Purchase Order No. X" are labels pointing at another document.
				rest := strings.TrimSpace(string([]rune(l)[words[j-1].End:]))
				isLabel := strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "#")
				isRef := isLabel || (j < len(words) && refFollowers[words[j].Text]) || (i > 0 && refLeaders[words[i-1].Text])
				// A title stands mostly alone on its line (maybe with the document number/date).
				standalone := len(words) <= j-i+3
				switch {
				case inTitle && !isRef && (standalone || n <= 3):
					if t.weight > titleHit[t.docType] {
						titleHit[t.docType] = t.weight
						evidence = append(evidence, fmt.Sprintf("%s: title %q", t.docType, strings.TrimSpace(l)))
					}
				case !isRef && standalone:
					bodyScores[t.docType] += 2 // e.g. repeated title on a later page
				default:
					bodyScores[t.docType] += 0.5 // a reference to that document type
				}
			}
		}
		for _, b := range body {
			if _, _, ok := FuzzyFind(words, b.phrase, fuzzyRatio); ok {
				bodyScores[b.docType] += b.weight
			}
		}
	}
	for k, v := range titleHit {
		scores[k] += v
	}
	for k, v := range bodyScores {
		scores[k] += min(v, bodyCap)
	}
	type kv struct {
		k string
		v float64
	}
	var ranked []kv
	for k, v := range scores {
		ranked = append(ranked, kv{k, v})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].v > ranked[j].v || (ranked[i].v == ranked[j].v && ranked[i].k < ranked[j].k)
	})
	if len(ranked) == 0 || ranked[0].v < 3 {
		return Result{DocumentType: "OTHER", Confidence: 0.2, Scores: scores, Evidence: append(evidence, "no recognisable document title or structure")}
	}
	top := ranked[0]
	second := 0.0
	if len(ranked) > 1 {
		second = ranked[1].v
	}
	// confidence: margin over the runner-up, and whether a title was seen at all
	conf := 0.45 + 0.5*(top.v-second)/top.v
	if titleHit[top.k] > 0 {
		conf += 0.15
	}
	if conf > 0.99 {
		conf = 0.99
	}
	if titleHit[top.k] == 0 {
		evidence = append(evidence, fmt.Sprintf("%s inferred from structure only (no title found)", top.k))
	}
	return Result{DocumentType: top.k, Confidence: float64(int(conf*100)) / 100, Scores: scores, Evidence: evidence}
}

func overlaps(spans [][2]int, i, j int) bool {
	for _, s := range spans {
		if i < s[1] && j > s[0] {
			return true
		}
	}
	return false
}
