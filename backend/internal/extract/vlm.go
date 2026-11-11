package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VLM extracts documents with a local vision-language model served by Ollama. The model only
// transcribes what is printed into a fixed JSON schema; all arithmetic, validation and decisions
// stay in our deterministic code (parse.FromVLM and the validate/match/decision packages).
type VLM struct {
	URL          string        // e.g. http://ollama:11434
	Model        string        // e.g. qwen3-vl:4b-instruct
	PageTimeout  time.Duration // per page (CPU inference is slow)
	MaxPx        int           // longest side of the rendered page sent to the model
	PdftoppmBin  string
	HTTP         *http.Client
	ContextLimit int // num_ctx
}

// VLMParty / VLMItem / VLMDoc mirror the JSON schema sent to the model.
type VLMParty struct {
	Name  *string `json:"name"`
	GSTIN *string `json:"gstin"`
}

type VLMItem struct {
	Line         *int     `json:"line"`
	ItemCode     *string  `json:"item_code"`
	Description  *string  `json:"description"`
	HSN          *string  `json:"hsn"`
	Unit         *string  `json:"unit"`
	Quantity     *float64 `json:"quantity"`
	UnitPrice    *float64 `json:"unit_price"`
	Discount     *float64 `json:"discount"`
	TaxableValue *float64 `json:"taxable_value"`
	TaxRate      *float64 `json:"tax_rate"`
	TaxAmount    *float64 `json:"tax_amount"`
	Amount       *float64 `json:"amount"`
	QtyReceived  *float64 `json:"qty_received"`
	QtyAccepted  *float64 `json:"qty_accepted"`
	QtyRejected  *float64 `json:"qty_rejected"`
	Page         int      `json:"-"`
}

type VLMDoc struct {
	DocumentType     string    `json:"document_type"`
	DocumentNumber   *string   `json:"document_number"`
	DocumentDate     *string   `json:"document_date"`
	DueDate          *string   `json:"due_date"`
	POReference      *string   `json:"po_reference"`
	InvoiceReference *string   `json:"invoice_reference"`
	Seller           VLMParty  `json:"seller"`
	Buyer            VLMParty  `json:"buyer"`
	Currency         *string   `json:"currency"`
	PaymentTerms     *string   `json:"payment_terms"`
	Items            []VLMItem `json:"items"`
	Subtotal         *float64  `json:"subtotal"`
	TaxableAmount    *float64  `json:"taxable_amount"`
	CGST             *float64  `json:"cgst"`
	SGST             *float64  `json:"sgst"`
	IGST             *float64  `json:"igst"`
	TotalTax         *float64  `json:"total_tax"`
	OtherCharges     *float64  `json:"other_charges"`
	RoundOff         *float64  `json:"round_off"`
	GrandTotal       *float64  `json:"grand_total"`

	// filled by the client, not the model
	Pages     int            `json:"-"`
	FieldPage map[string]int `json:"-"` // first page each header field was found on
	ModelName string         `json:"-"`
	Duration  time.Duration  `json:"-"`
}

var (
	str       = map[string]any{"type": []string{"string", "null"}}
	num       = map[string]any{"type": []string{"number", "null"}}
	party     = map[string]any{"type": "object", "properties": map[string]any{"name": str, "gstin": str}}
	vlmSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"document_type":     map[string]any{"type": "string", "enum": []string{"INVOICE", "PO", "GRN", "EWAY_BILL", "MRN", "OTHER"}},
			"document_number":   str,
			"document_date":     str,
			"due_date":          str,
			"po_reference":      str,
			"invoice_reference": str,
			"seller":            party,
			"buyer":             party,
			"currency":          str,
			"payment_terms":     str,
			"items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"line": map[string]any{"type": []string{"integer", "null"}}, "item_code": str, "description": str, "hsn": str, "unit": str,
				"quantity": num, "unit_price": num, "discount": num, "taxable_value": num, "tax_rate": num, "tax_amount": num,
				"amount": num, "qty_received": num, "qty_accepted": num, "qty_rejected": num,
			}}},
			"subtotal": num, "taxable_amount": num, "cgst": num, "sgst": num, "igst": num, "total_tax": num,
			"other_charges": num, "round_off": num, "grand_total": num,
		},
		// Every key is required (values may be null): models tend to silently omit optional keys,
		// so forcing an explicit answer per field makes "not printed" an explicit null.
		"required": []string{"document_type", "document_number", "document_date", "due_date", "po_reference", "invoice_reference",
			"seller", "buyer", "currency", "payment_terms", "items", "subtotal", "taxable_amount", "cgst", "sgst", "igst",
			"total_tax", "other_charges", "round_off", "grand_total"},
	}
)

func init() {
	// same rule inside nested objects
	party["required"] = []string{"name", "gstin"}
	items := vlmSchema["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
	items["required"] = []string{"line", "item_code", "description", "hsn", "unit", "quantity", "unit_price", "discount",
		"taxable_value", "tax_rate", "tax_amount", "amount", "qty_received", "qty_accepted", "qty_rejected"}
}

const vlmPrompt = `You are reading page %d of %d of an Indian business document: a tax invoice, purchase order (PO),
goods receipt note (GRN), e-way bill or material return note (MRN). Transcribe it into the JSON schema.

Rules:
- Only transcribe. Never calculate, correct, round or invent a value. If something is not printed on this page, use null.
- Copy identifiers exactly as printed: document numbers, GSTINs (15 characters), PO numbers, item codes, HSN codes.
- document_number is THIS document's own number (invoice number, PO number or GRN number), not a reference to another document.
- seller = the supplier / vendor / consignor. buyer = the purchasing company / bill-to / consignee.
- Dates as YYYY-MM-DD. Printed numeric dates are day-first: 23/11/2022 means 2022-11-23.
- Amounts as plain numbers: no currency symbols, no thousands separators (₹1,23,456.50 -> 123456.5).
- items: one entry per table row, in printed order. Join a description that wraps over several lines into one row.
  Ignore TOTAL / SUB-TOTAL rows: they are not items.
  quantity = billed/ordered quantity; unit_price = rate per unit; taxable_value = amount before tax;
  amount = the row's last amount column; tax_rate in percent (18 for 18%%).
  For goods receipt notes also fill qty_received, qty_accepted and qty_rejected.
- Header totals (subtotal, cgst, sgst, igst, total_tax, round_off, grand_total) only if printed on this page.%s`

// Extract renders every page, transcribes each one and merges the result.
func (v *VLM) Extract(ctx context.Context, path, mime, typeHint string) (*VLMDoc, error) {
	dir, err := os.MkdirTemp("", "vlm-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	pages, err := v.render(ctx, path, mime, dir)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	merged := &VLMDoc{Pages: len(pages), FieldPage: map[string]int{}, ModelName: v.Model}
	for i, p := range pages {
		hint := ""
		if typeHint != "" && typeHint != "OTHER" {
			hint = "\nThe uploader says this is a " + typeHint + "; use document_type " + typeHint + " unless the page clearly says otherwise."
		}
		doc, err := v.page(ctx, p, i+1, len(pages), hint)
		if err != nil {
			return nil, fmt.Errorf("vision model, page %d: %w", i+1, err)
		}
		mergePage(merged, doc, i+1)
	}
	merged.Duration = time.Since(start)
	return merged, nil
}

func (v *VLM) render(ctx context.Context, path, mime, dir string) ([]string, error) {
	maxPx := v.MaxPx
	if maxPx == 0 {
		maxPx = 1600
	}
	if mime != "application/pdf" {
		return []string{path}, nil // images are sent as uploaded
	}
	if _, err := run(ctx, 2*time.Minute, v.PdftoppmBin, "-png", "-scale-to", fmt.Sprint(maxPx), path, filepath.Join(dir, "p")); err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p*.png"))
	sort.Strings(files) // p-1.png, p-2.png ... (zero-padded by pdftoppm for 10+ pages)
	if len(files) == 0 {
		return nil, errors.New("no pages rendered")
	}
	return files, nil
}

func (v *VLM) page(ctx context.Context, imgPath string, n, total int, hint string) (*VLMDoc, error) {
	img, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, err
	}
	numCtx := v.ContextLimit
	if numCtx == 0 {
		numCtx = 16384
	}
	body, _ := json.Marshal(map[string]any{
		"model":   v.Model,
		"stream":  false,
		"format":  vlmSchema,
		"options": map[string]any{"temperature": 0, "num_ctx": numCtx, "num_predict": 6144},
		"messages": []map[string]any{{
			"role": "user", "content": fmt.Sprintf(vlmPrompt, n, total, hint),
			"images": []string{base64.StdEncoding.EncodeToString(img)},
		}},
	})
	timeout := v.PageTimeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(pctx, http.MethodPost, strings.TrimRight(v.URL, "/")+"/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := v.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable at %s (is the ollama container running?): %w", v.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama response: %w", err)
	}
	var doc VLMDoc
	if err := json.Unmarshal([]byte(out.Message.Content), &doc); err != nil {
		return nil, fmt.Errorf("model did not return valid JSON: %w", err)
	}
	return &doc, nil
}

// mergePage combines per-page results: header fields from the first page that has them,
// items appended in page order, totals from the last page that prints them.
func mergePage(m, p *VLMDoc, page int) {
	firstStr := func(dst **string, src *string, key string) {
		if *dst == nil && src != nil && strings.TrimSpace(*src) != "" {
			*dst = src
			m.FieldPage[key] = page
		}
	}
	lastNum := func(dst **float64, src *float64, key string) {
		if src != nil {
			*dst = src
			m.FieldPage[key] = page
		}
	}
	if m.DocumentType == "" || m.DocumentType == "OTHER" {
		m.DocumentType = p.DocumentType
	}
	firstStr(&m.DocumentNumber, p.DocumentNumber, "document_number")
	firstStr(&m.DocumentDate, p.DocumentDate, "document_date")
	firstStr(&m.DueDate, p.DueDate, "due_date")
	firstStr(&m.POReference, p.POReference, "po_reference")
	firstStr(&m.InvoiceReference, p.InvoiceReference, "invoice_reference")
	firstStr(&m.Seller.Name, p.Seller.Name, "seller_name")
	firstStr(&m.Seller.GSTIN, p.Seller.GSTIN, "seller_gstin")
	firstStr(&m.Buyer.Name, p.Buyer.Name, "buyer_name")
	firstStr(&m.Buyer.GSTIN, p.Buyer.GSTIN, "buyer_gstin")
	firstStr(&m.Currency, p.Currency, "currency")
	firstStr(&m.PaymentTerms, p.PaymentTerms, "payment_terms")
	for _, it := range p.Items {
		it.Page = page
		m.Items = append(m.Items, it)
	}
	lastNum(&m.Subtotal, p.Subtotal, "subtotal")
	lastNum(&m.TaxableAmount, p.TaxableAmount, "taxable_amount")
	lastNum(&m.CGST, p.CGST, "cgst")
	lastNum(&m.SGST, p.SGST, "sgst")
	lastNum(&m.IGST, p.IGST, "igst")
	lastNum(&m.TotalTax, p.TotalTax, "total_tax")
	lastNum(&m.OtherCharges, p.OtherCharges, "other_charges")
	lastNum(&m.RoundOff, p.RoundOff, "round_off")
	lastNum(&m.GrandTotal, p.GrandTotal, "grand_total")
}

// ExtractTextLayer returns only the text embedded in a PDF (no OCR). Pages without a text layer
// are skipped; images yield an empty result. Used to cross-check the vision model's output.
func (e *Extractor) ExtractTextLayer(ctx context.Context, path, mime string) (*Result, error) {
	if mime != "application/pdf" {
		return &Result{Method: MethodPDFText, PageCount: 1}, nil
	}
	out, err := run(ctx, 2*time.Minute, e.PdftotextBin, "-layout", "-enc", "UTF-8", path, "-")
	if err != nil {
		return nil, err
	}
	pages := strings.Split(string(out), "\f")
	if len(pages) > 1 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	res := &Result{Method: MethodPDFText, PageCount: len(pages), MeanConfidence: 100}
	for i, p := range pages {
		if alnumCount(p) < e.MinPageChars {
			res.OCRPages = append(res.OCRPages, i+1) // here: pages WITHOUT a usable text layer
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
