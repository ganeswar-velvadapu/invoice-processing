package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// numberRe matches money/quantity tokens: 1,23,456.78  1234  (1,234.00)  -12.5  ₹1,000
var numberRe = regexp.MustCompile(`\(?-?(?:₹|Rs\.?|INR)?\s?\d[\d,]*(?:\.\d+)?\)?`)

// ParseNumber parses an Indian/Western formatted number. Returns ok=false for non-numbers.
func ParseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = strings.Trim(s, "()")
	}
	for _, p := range []string{"₹", "Rs.", "Rs", "INR", "$", "€"} {
		s = strings.TrimPrefix(strings.TrimSpace(s), p)
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(strings.ToUpper(s), "CR") {
		s = strings.TrimSpace(s[:len(s)-2])
	}
	if strings.HasPrefix(s, "-") {
		neg = !neg
		s = s[1:]
	}
	s = strings.TrimSuffix(s, "%")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, false
	}
	// OCR: letter O inside digits
	if strings.ContainsAny(s, "Oo") {
		s = strings.NewReplacer("O", "0", "o", "0").Replace(s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// numericCell reports whether a whole cell is a number (optionally with %, currency or a unit suffix).
var numericCellRe = regexp.MustCompile(`^\(?-?(?:₹|Rs\.?|INR)?\s?\d[\d,]*(?:\.\d+)?\)?\s?%?$`)

func IsNumericCell(s string) bool { return numericCellRe.MatchString(strings.TrimSpace(s)) }

// qtyWithUnit: "10 Nos", "12.5 KG"
var qtyUnitRe = regexp.MustCompile(`^(\d[\d,]*(?:\.\d+)?)\s*([A-Za-z]{1,6}\.?)$`)

func splitQtyUnit(s string) (float64, string, bool) {
	m := qtyUnitRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, "", false
	}
	f, ok := ParseNumber(m[1])
	return f, strings.TrimSuffix(m[2], "."), ok
}

var months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7,
	"aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12}

// DateRe finds the common Indian invoice date formats.
var DateRe = regexp.MustCompile(`(?i)\b(?:\d{1,2}[/.\-]\d{1,2}[/.\-]\d{2,4}|\d{4}-\d{2}-\d{2}|\d{1,2}[\s\-]*(?:jan|feb|mar|apr|may|jun|jul|aug|sept?|oct|nov|dec)[a-z]*[\s\-,]*\d{2,4}|(?:jan|feb|mar|apr|may|jun|jul|aug|sept?|oct|nov|dec)[a-z]*\s+\d{1,2},?\s+\d{4})\b`)

var dmyRe = regexp.MustCompile(`^(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{2,4})$`)
var ymdRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
var dMonYRe = regexp.MustCompile(`(?i)^(\d{1,2})[\s\-]*([a-z]+)[\s\-,]*(\d{2,4})$`)
var monDYRe = regexp.MustCompile(`(?i)^([a-z]+)\s+(\d{1,2}),?\s+(\d{4})$`)

// ParseDate parses a date token assuming day-first ordering for numeric dates (Indian convention).
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	mk := func(y, m, d int) (time.Time, bool) {
		if y < 100 {
			y += 2000
		}
		if m < 1 || m > 12 || d < 1 || d > 31 || y < 1990 || y > 2100 {
			return time.Time{}, false
		}
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if t.Day() != d {
			return time.Time{}, false
		}
		return t, true
	}
	atoi := func(x string) int { v, _ := strconv.Atoi(x); return v }
	if m := ymdRe.FindStringSubmatch(s); m != nil {
		return mk(atoi(m[1]), atoi(m[2]), atoi(m[3]))
	}
	if m := dmyRe.FindStringSubmatch(s); m != nil {
		d, mo := atoi(m[1]), atoi(m[2])
		if mo > 12 && d <= 12 { // clearly month-first
			d, mo = mo, d
		}
		return mk(atoi(m[3]), mo, d)
	}
	if m := dMonYRe.FindStringSubmatch(s); m != nil {
		mon, ok := monthOf(m[2])
		if !ok {
			return time.Time{}, false
		}
		return mk(atoi(m[3]), int(mon), atoi(m[1]))
	}
	if m := monDYRe.FindStringSubmatch(s); m != nil {
		mon, ok := monthOf(m[1])
		if !ok {
			return time.Time{}, false
		}
		return mk(atoi(m[3]), int(mon), atoi(m[2]))
	}
	return time.Time{}, false
}

func monthOf(s string) (time.Month, bool) {
	s = strings.ToLower(s)
	if len(s) > 4 {
		if m, ok := months[s[:4]]; ok {
			return m, true
		}
		s = s[:3]
	}
	m, ok := months[s]
	return m, ok
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
