// Package gstin validates Indian GSTINs (format + mod-36 checksum). It does not verify registration status.
package gstin

import (
	"regexp"
	"strings"
)

const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

var formatRe = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

// Loose pattern used to find candidates in noisy text (OCR may confuse 0/O, 1/I, 5/S, 8/B).
var FindRe = regexp.MustCompile(`\b[0-9OISB]{2}[A-Z0-9]{5}[0-9OISBZ]{4}[A-Z0-9][A-Z0-9]{1}[Z2][A-Z0-9]\b`)

func Checksum(first14 string) byte {
	sum := 0
	for i := 0; i < 14; i++ {
		v := strings.IndexByte(charset, first14[i])
		if v < 0 {
			return 0
		}
		f := 1
		if i%2 == 1 {
			f = 2
		}
		p := v * f
		sum += p/36 + p%36
	}
	return charset[(36-sum%36)%36]
}

type Result struct {
	Valid       bool   `json:"valid"`
	FormatValid bool   `json:"format_valid"`
	Reason      string `json:"reason"`
	StateCode   string `json:"state_code,omitempty"`
}

func Validate(g string) Result {
	g = Normalize(g)
	if len(g) != 15 {
		return Result{Reason: "GSTIN must be 15 characters"}
	}
	if !formatRe.MatchString(g) {
		return Result{Reason: "GSTIN does not match the required pattern"}
	}
	st := g[:2]
	if !(st >= "01" && st <= "38") && st != "97" && st != "99" {
		return Result{FormatValid: true, Reason: "invalid state code " + st}
	}
	if Checksum(g[:14]) != g[14] {
		return Result{FormatValid: true, StateCode: st, Reason: "checksum digit mismatch"}
	}
	return Result{Valid: true, FormatValid: true, StateCode: st, Reason: "format and checksum valid"}
}

func Normalize(g string) string {
	return strings.ToUpper(strings.Join(strings.Fields(g), ""))
}

var toDigit = map[byte]byte{'O': '0', 'D': '0', 'Q': '0', 'I': '1', 'L': '1', 'S': '5', 'B': '8', 'Z': '2', 'G': '6'}
var toLetter = map[byte]byte{'0': 'O', '1': 'I', '5': 'S', '8': 'B', '2': 'Z', '6': 'G'}

// Repair fixes common OCR confusions using position-specific character classes and accepts the
// result only if the checksum validates. Returns the repaired GSTIN and whether it changed.
func Repair(g string) (string, bool) {
	g = Normalize(g)
	if len(g) != 15 || Validate(g).Valid {
		return g, false
	}
	b := []byte(g)
	digitPos := map[int]bool{0: true, 1: true, 7: true, 8: true, 9: true, 10: true}
	letterPos := map[int]bool{2: true, 3: true, 4: true, 5: true, 6: true, 11: true}
	for i := range b {
		if digitPos[i] {
			if d, ok := toDigit[b[i]]; ok {
				b[i] = d
			}
		} else if letterPos[i] {
			if l, ok := toLetter[b[i]]; ok {
				b[i] = l
			}
		} else if i == 13 && b[i] == '2' {
			b[i] = 'Z'
		}
	}
	r := string(b)
	if Validate(r).Valid {
		return r, true
	}
	return g, false
}

// WithChecksum appends the correct checksum (useful for generating fixtures).
func WithChecksum(first14 string) string { return first14 + string(Checksum(first14)) }

// Checksum policy (GSTIN_CHECKSUM): "strict" (default) rejects a checksum mismatch; "warn" and "off"
// accept a GSTIN whose format is valid but whose checksum digit is wrong (e.g. synthetic test data).
// "warn" lets validation raise a warning, "off" ignores it. Format and state code are always enforced.
var checksumMode = "strict"

// SetChecksumMode sets the process-wide checksum policy; unknown values mean strict.
func SetChecksumMode(m string) {
	switch m {
	case "warn", "off":
		checksumMode = m
	default:
		checksumMode = "strict"
	}
}

func ChecksumMode() string { return checksumMode }

// Check is Validate with the checksum policy applied. ChecksumIgnored is set when the GSTIN is
// accepted only because the policy tolerates a checksum mismatch.
func Check(g string) (r Result, checksumIgnored bool) {
	r = Validate(g)
	if !r.Valid && r.FormatValid && r.StateCode != "" && checksumMode != "strict" {
		r.Valid = true
		r.Reason = "format valid; checksum digit does not match (accepted: GSTIN_CHECKSUM=" + checksumMode + ")"
		return r, true
	}
	return r, false
}
