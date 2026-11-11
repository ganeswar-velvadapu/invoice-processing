package classify

import (
	"strings"
	"unicode"
)

// Word is a normalised word with its rune span in the original line.
type Word struct {
	Text       string
	Start, End int
}

// ocrFold maps digits OCR commonly produces inside words back to letters.
var ocrFold = map[rune]rune{'0': 'o', '1': 'l', '5': 's', '8': 'b', '6': 'g', '2': 'z', '|': 'l'}

// Words splits a line into lowercase letter-only words (digits folded to look-alike letters
// when they sit inside a word), keeping rune positions for value extraction after a label.
func Words(line string) []Word {
	r := []rune(line)
	var out []Word
	i := 0
	for i < len(r) {
		for i < len(r) && !isWordRune(r[i]) {
			i++
		}
		s := i
		var b strings.Builder
		letters := 0
		for i < len(r) && isWordRune(r[i]) {
			c := unicode.ToLower(r[i])
			if unicode.IsLetter(c) {
				letters++
			}
			b.WriteRune(c)
			i++
		}
		if i > s {
			w := b.String()
			if letters == 0 {
				continue // pure number: not a word
			}
			if letters < len([]rune(w)) {
				var f strings.Builder
				for _, c := range w {
					if m, ok := ocrFold[c]; ok {
						c = m
					}
					if unicode.IsLetter(c) {
						f.WriteRune(c)
					}
				}
				w = f.String()
			}
			out = append(out, Word{Text: w, Start: s, End: i})
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '|'
}

// Levenshtein distance on runes.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// FuzzyFind looks for phrase (lowercase words separated by single spaces) in words, tolerating
// OCR errors: the window of the same word count must be within maxRatio edit distance of the
// phrase. Short phrases (< 5 letters) must match exactly. Returns the matched word index range.
func FuzzyFind(words []Word, phrase string, maxRatio float64) (int, int, bool) {
	pw := strings.Fields(phrase)
	n := len(pw)
	if n == 0 || len(words) < n {
		return 0, 0, false
	}
	plen := len([]rune(phrase))
	allowed := int(float64(plen) * maxRatio)
	if plen < 5 {
		allowed = 0
	}
	for i := 0; i+n <= len(words); i++ {
		parts := make([]string, n)
		for k := 0; k < n; k++ {
			parts[k] = words[i+k].Text
		}
		cand := strings.Join(parts, " ")
		if cand == phrase {
			return i, i + n, true
		}
		if allowed == 0 || abs(len([]rune(cand))-plen) > allowed {
			continue
		}
		// each word must still resemble its counterpart (avoid "purchase coke" ≈ "purchase order")
		ok := true
		for k := 0; k < n; k++ {
			wl := len([]rune(pw[k]))
			limit := max(1, wl/3)
			if wl <= 3 {
				limit = 0 // short words ("no", "po", "nov") must match exactly
			}
			if Levenshtein(parts[k], pw[k]) > limit {
				ok = false
				break
			}
		}
		if ok && Levenshtein(cand, phrase) <= allowed {
			return i, i + n, true
		}
	}
	return 0, 0, false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
