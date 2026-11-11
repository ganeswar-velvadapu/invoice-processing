// Package extract turns a stored PDF/image into layout-preserving text lines.
// PDF embedded text is used first; pages without usable text are rendered and OCR'd with Tesseract.
// OCR sits behind the OCREngine interface so it can be swapped without touching parsing or matching.
package extract

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	MethodPDFText = "PDF_TEXT"
	MethodOCR     = "OCR"
)

// Line is one visual line of text. BBox is [x0,y0,x1,y1] in page pixels when known (OCR only).
type Line struct {
	Page int     `json:"page"`
	Text string  `json:"text"`
	BBox *[4]int `json:"bbox,omitempty"`
	Conf float64 `json:"conf,omitempty"` // mean OCR word confidence 0-100, 0 if not OCR
}

type Result struct {
	Method         string // PDF_TEXT if every page had text, else OCR (mixed counts as OCR)
	PageCount      int
	Lines          []Line
	OCRPages       []int
	MeanConfidence float64 // mean OCR confidence across OCR'd words (0-100); 100 for pure PDF text
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

// OCREngine recognises one page image.
type OCREngine interface {
	RecognizeImage(ctx context.Context, imagePath string, page int) ([]Line, float64, error)
}

type Extractor struct {
	PdftotextBin string
	PdftoppmBin  string
	OCR          OCREngine
	DPI          int
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

func (e *Extractor) Extract(ctx context.Context, path, mime string) (*Result, error) {
	if mime == "application/pdf" {
		return e.extractPDF(ctx, path)
	}
	if e.OCR == nil {
		return nil, fmt.Errorf("OCR engine not configured")
	}
	lines, conf, err := e.OCR.RecognizeImage(ctx, path, 1)
	if err != nil {
		return nil, err
	}
	return &Result{Method: MethodOCR, PageCount: 1, Lines: lines, OCRPages: []int{1}, MeanConfidence: conf}, nil
}

func (e *Extractor) extractPDF(ctx context.Context, path string) (*Result, error) {
	out, err := run(ctx, 2*time.Minute, e.PdftotextBin, "-layout", "-enc", "UTF-8", path, "-")
	if err != nil {
		return nil, err
	}
	pages := strings.Split(string(out), "\f")
	if len(pages) > 1 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	res := &Result{Method: MethodPDFText, PageCount: len(pages)}
	var confSum float64
	var confN int
	for i, p := range pages {
		pageNo := i + 1
		if alnumCount(p) >= e.MinPageChars {
			for _, l := range strings.Split(p, "\n") {
				if strings.TrimSpace(l) != "" {
					res.Lines = append(res.Lines, Line{Page: pageNo, Text: strings.TrimRight(l, " \t")})
				}
			}
			continue
		}
		if e.OCR == nil {
			return nil, fmt.Errorf("page %d has no embedded text and OCR is not configured", pageNo)
		}
		lines, conf, err := e.ocrPDFPage(ctx, path, pageNo)
		if err != nil {
			return nil, fmt.Errorf("ocr page %d: %w", pageNo, err)
		}
		res.Lines = append(res.Lines, lines...)
		res.OCRPages = append(res.OCRPages, pageNo)
		confSum += conf
		confN++
	}
	if confN > 0 {
		res.Method = MethodOCR
		res.MeanConfidence = confSum / float64(confN)
	} else {
		res.MeanConfidence = 100
	}
	return res, nil
}

func (e *Extractor) ocrPDFPage(ctx context.Context, path string, page int) ([]Line, float64, error) {
	dir, err := os.MkdirTemp("", "ocr-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	dpi := e.DPI
	if dpi == 0 {
		dpi = 300
	}
	prefix := filepath.Join(dir, "p")
	pg := fmt.Sprint(page)
	if _, err := run(ctx, 2*time.Minute, e.PdftoppmBin, "-r", fmt.Sprint(dpi), "-gray", "-png",
		"-f", pg, "-l", pg, "-singlefile", path, prefix); err != nil {
		return nil, 0, err
	}
	return e.OCR.RecognizeImage(ctx, prefix+".png", page)
}
