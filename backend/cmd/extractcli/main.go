// Command extractcli runs classification + extraction on a local file and prints the JSON result.
// Useful for tuning the parser without the API: go run ./cmd/extractcli file.pdf
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"invoiceproc/internal/classify"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
	"invoiceproc/internal/parse"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: extractcli FILE [--text]")
		os.Exit(2)
	}
	path := os.Args[1]
	gstin.SetChecksumMode(os.Getenv("GSTIN_CHECKSUM"))
	mime := "application/pdf"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		mime = "image/png"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".tif", ".tiff":
		mime = "image/tiff"
	}
	ex := &extract.Extractor{PdftotextBin: "pdftotext", PdftoppmBin: "pdftoppm", MinPageChars: 40,
		OCR: extract.NewEngine(envOr("OCR_ENGINE", "tesseract"), "tesseract", "eng", envOr("PADDLE_PYTHON", "python3"),
			envOr("PADDLE_OCR_SCRIPT", "ocr/paddle_ocr_server.py"), envOr("PADDLE_OCR_MODEL", "mobile"),
			func(err error) { fmt.Fprintln(os.Stderr, "paddleocr failed, using tesseract:", err) })}
	r, err := ex.Extract(context.Background(), path, mime)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) > 2 && os.Args[2] == "--text" {
		fmt.Print(r.Text())
	}
	var texts []string
	for _, l := range r.Lines {
		texts = append(texts, l.Text)
	}
	c := classify.Classify(texts)
	// BUYER_GSTINS / VENDOR_GSTINS (comma-separated) stand in for config and the vendor master.
	d := parse.Parse(r.Lines, c.DocumentType, parse.Options{BuyerGSTINs: list("BUYER_GSTINS"), VendorGSTINs: list("VENDOR_GSTINS")})
	d.ClassificationConfidence = c.Confidence
	out := map[string]any{"reconcile_problems": parse.ReconcileProblems(d, r.Lines), "method": r.Method, "ocr_conf": r.MeanConfidence, "classification": c, "data": d,
		"overall_confidence": parse.OverallConfidence(d), "low_confidence": parse.LowConfidence(d, 0.75)}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func list(env string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(env), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
