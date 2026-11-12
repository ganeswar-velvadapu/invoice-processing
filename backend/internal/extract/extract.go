// Package extract turns a stored document into layout-preserving text lines and, for scans, hands
// page images to the local vision model (vlm.go). There is no OCR: a digital PDF's own text layer is
// read with pdftotext; documents without one are read by the vision model.
package extract

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const MethodPDFText = "PDF_TEXT"

// Line is one visual line of text. BBox and Conf are optional (unused for PDF text).
type Line struct {
	Page int     `json:"page"`
	Text string  `json:"text"`
	BBox *[4]int `json:"bbox,omitempty"`
	Conf float64 `json:"conf,omitempty"`
}

type Result struct {
	Method       string
	PageCount    int
	Lines        []Line
	ScannedPages []int // pages without a usable text layer (scans): only the vision model can read them
}

func (r *Result) Text() string {
	var b strings.Builder
	page := 1
	for _, l := range r.Lines {
		if l.Page != page {
			b.WriteString("\f")
			page = l.Page
		}
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// FullTextLayer reports whether every page has embedded text.
func (r *Result) FullTextLayer() bool { return len(r.Lines) > 0 && len(r.ScannedPages) == 0 }

type Extractor struct {
	PdftotextBin string
	// MinPageChars is the number of alphanumeric chars below which a page is treated as scanned.
	MinPageChars int
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func alnumCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

// Extract returns the text embedded in a PDF. Pages without a text layer are listed in ScannedPages;
// an image file is one scanned page with no text.
func (e *Extractor) Extract(ctx context.Context, path, mime string) (*Result, error) {
	if mime != "application/pdf" {
		return &Result{Method: MethodPDFText, PageCount: 1, ScannedPages: []int{1}}, nil
	}
	out, err := run(ctx, 2*time.Minute, e.PdftotextBin, "-layout", "-enc", "UTF-8", path, "-")
	if err != nil {
		return nil, err
	}
	pages := strings.Split(string(out), "\f")
	if len(pages) > 1 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	res := &Result{Method: MethodPDFText, PageCount: len(pages)}
	for i, p := range pages {
		if alnumCount(p) < e.MinPageChars {
			res.ScannedPages = append(res.ScannedPages, i+1)
			continue
		}
		for _, l := range strings.Split(p, "\n") {
			if strings.TrimSpace(l) != "" {
				res.Lines = append(res.Lines, Line{Page: i + 1, Text: strings.TrimRight(l, " \t")})
			}
		}
	}
	return res, nil
}
