// Package parse extracts structured header fields and line items from layout-preserving text.
// It is layout-agnostic: fields are located through label anchors, value patterns and
// arithmetic cross-checks; tables are found by header keywords with an arithmetic fallback.
// Vendor-specific regex profiles can override any header field.
package parse

import (
	"invoiceproc/internal/extract"
)

// Field is one extracted header value. Values are normalised strings:
// dates as YYYY-MM-DD, numbers as plain decimals, lists comma-separated.
type Field struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Page       int     `json:"page,omitempty"`
	BBox       *[4]int `json:"bbox,omitempty"`
	Source     string  `json:"source"` // anchor, fallback, computed, profile, manual
}

// Item is one table row. Quantity fields are pointers so "not present" differs from zero.
type Item struct {
	LineNumber       int      `json:"line_number"`
	ItemCode         string   `json:"item_code"`
	Description      string   `json:"description"`
	HSNSAC           string   `json:"hsn_sac"`
	Quantity         *float64 `json:"quantity"`
	Unit             string   `json:"unit"`
	UnitPrice        *float64 `json:"unit_price"`
	Discount         *float64 `json:"discount"`
	TaxableValue     *float64 `json:"taxable_value"`
	TaxRate          *float64 `json:"tax_rate"`
	TaxAmount        *float64 `json:"tax_amount"`
	LineTotal        *float64 `json:"line_total"`
	QuantityReceived *float64 `json:"quantity_received,omitempty"`
	QuantityAccepted *float64 `json:"quantity_accepted,omitempty"`
	QuantityRejected *float64 `json:"quantity_rejected,omitempty"`
	Confidence       float64  `json:"confidence"`
	Page             int      `json:"page,omitempty"`
	Source           string   `json:"source"` // table, arithmetic, manual
	Raw              string   `json:"raw,omitempty"`

	discountPct *float64
}

type Data struct {
	DocumentType string `json:"document_type"`
	// ClassificationConfidence < 0.6 (or type OTHER) sends the document to review so a person confirms the type.
	ClassificationConfidence float64           `json:"classification_confidence,omitempty"`
	Fields                   map[string]*Field `json:"fields"`
	Items                    []Item            `json:"items"`
	Warnings                 []string          `json:"warnings"`
	// ExtractedBy names the engine/model that produced this version (e.g. "vlm:qwen3-vl:4b-instruct").
	ExtractedBy string `json:"extracted_by,omitempty"`
}

// Profile holds vendor-specific regex overrides: field name -> regex with one capture group.
type Profile map[string]string

// Options influences seller/buyer disambiguation.
type Options struct {
	BuyerGSTINs  []string // our own GSTINs; never the seller on an invoice
	VendorGSTINs []string // vendor master GSTINs: used to repair OCR damage and to tell vendor from buyer
	Profile      Profile
}

// CriticalFields per document type: missing or low-confidence values route to human review.
var CriticalFields = map[string][]string{
	"INVOICE": {"invoice_number", "invoice_date", "seller_gstin", "grand_total"},
	"PO":      {"po_number", "vendor_gstin"},
	"GRN":     {"grn_number", "po_reference"},
}

type doc struct {
	lines []extract.Line
}
