// Package util holds small helpers shared across packages: normalisation, rounding, nullable values.
package utils

import (
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var nonAlnum = regexp.MustCompile(`[^A-Z0-9]`)

// NormalizeRef uppercases and strips everything except letters/digits ("po-101 " -> "PO101").
func NormalizeRef(s string) string {
	return nonAlnum.ReplaceAllString(strings.ToUpper(strings.TrimSpace(s)), "")
}

// NormalizeDocNumber is like NormalizeRef but also drops leading zeros in numeric runs
// so "INV-0042" and "INV42" compare equal.
func NormalizeDocNumber(s string) string {
	n := NormalizeRef(s)
	var b strings.Builder
	prevDigit := false
	leading := true
	for _, r := range n {
		isDigit := r >= '0' && r <= '9'
		if isDigit {
			if !prevDigit {
				leading = true
			}
			if leading && r == '0' {
				prevDigit = true
				continue
			}
			leading = false
		} else if prevDigit && leading {
			b.WriteByte('0') // the run was all zeros
		}
		if !isDigit {
			leading = false
		}
		prevDigit = isDigit
		b.WriteRune(r)
	}
	if prevDigit && leading {
		b.WriteByte('0')
	}
	return b.String()
}

func Round2(f float64) float64 { return math.Round(f*100) / 100 }
func Round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// FinancialYear returns the Indian FY ("2025-26") for a date (April–March).
func FinancialYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

func NullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func NullF(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func NullTime(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}

func PtrF(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func F(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func Fp(f float64) *float64 { return &f }

// Tokens returns lowercased alphanumeric word tokens of length >= 2.
func Tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := f[:0]
	for _, t := range f {
		if len(t) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

// Jaccard similarity of word tokens.
func TextSimilarity(a, b string) float64 {
	ta, tb := Tokens(a), Tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	inter, union := 0, len(set)
	seen := map[string]bool{}
	for _, t := range tb {
		if seen[t] {
			continue
		}
		seen[t] = true
		if set[t] {
			inter++
		} else {
			union++
		}
	}
	return float64(inter) / float64(union)
}

// FormatQty prints a quantity without trailing zeros.
func FormatQty(f float64) string {
	return strconv.FormatFloat(Round3(f), 'f', -1, 64)
}
