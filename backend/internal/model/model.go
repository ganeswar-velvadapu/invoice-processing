// Package model holds in-memory domain types shared by validation, matching and decision logic.
// These are plain structs so the rule engines are pure and unit-testable without a database.
package model

import "time"

const RuleVersion = "R2026.11-1"

type Tolerance struct {
	Version                 int     `json:"version"`
	PricePct                float64 `json:"price_tolerance_pct"`
	PriceAbs                float64 `json:"price_tolerance_abs"`
	QuantityPct             float64 `json:"quantity_tolerance_pct"`
	TaxRoundingAbs          float64 `json:"tax_rounding_abs"`
	DateToleranceDays       int     `json:"date_tolerance_days"`
	MinExtractionConfidence float64 `json:"min_extraction_confidence"`
}

type Vendor struct {
	VendorID   string
	LegalName  string
	GSTIN      string
	IsApproved bool
	Status     string
}

type InvoiceItem struct {
	InvoiceItemID string
	LineNumber    int
	ItemCode      string
	Description   string
	HSNSAC        string
	Quantity      *float64
	Unit          string
	UnitPrice     *float64
	Discount      float64
	TaxableValue  *float64
	TaxRate       *float64
	TaxAmount     *float64
	LineTotal     *float64
}

type Invoice struct {
	InvoiceID            string
	DocumentID           string
	InvoiceNumber        string
	InvoiceDate          *time.Time
	DueDate              *time.Time
	VendorID             string
	SellerName           string
	SellerGSTIN          string
	BuyerName            string
	BuyerGSTIN           string
	POReferences         []string
	Currency             string
	Subtotal             *float64
	TaxableAmount        *float64
	CGST, SGST, IGST     *float64
	OtherCharges         *float64
	RoundOff             *float64
	TotalTax             *float64
	GrandTotal           *float64
	ExtractionConfidence float64
	Items                []InvoiceItem
}

type POItem struct {
	POItemID        string
	PurchaseOrderID string
	LineNumber      int
	ItemCode        string
	Description     string
	HSNSAC          string
	QuantityOrdered float64
	Unit            string
	UnitPrice       float64
	TaxRate         float64
	// Derived context for reconciliation:
	QuantityAccepted      float64 // sum of accepted quantity across GRNs linked to this line
	HasReceipt            bool    // at least one GRN line maps to this PO line
	QuantityInvoicedPrior float64 // allocated to other live invoices
}

type PurchaseOrder struct {
	PurchaseOrderID string
	PONumber        string
	VendorID        string
	VendorGSTIN     string
	PODate          *time.Time
	Status          string
	Items           []POItem
	GRNIDs          []string
}

// Check is one persisted validation/matching result.
type Check struct {
	Code          string         `json:"check"`
	Status        string         `json:"status"` // PASS, FAIL, WARNING, NOT_RUN
	Expected      string         `json:"expected,omitempty"`
	Actual        string         `json:"actual,omitempty"`
	Reason        string         `json:"reason"`
	Evidence      map[string]any `json:"evidence,omitempty"`
	Class         string         `json:"class,omitempty"` // decision class when not PASS (see decision package)
	ExceptionCode string         `json:"exception_code,omitempty"`
}

const (
	Pass    = "PASS"
	Fail    = "FAIL"
	Warning = "WARNING"
	NotRun  = "NOT_RUN"
)

// Decision classes, in precedence order (see decision.Decide).
const (
	ClassReject     = "REJECTING"   // confirmed policy violation
	ClassDuplicate  = "DUPLICATE"   // confirmed duplicate
	ClassMissing    = "MISSING"     // mandatory information missing
	ClassPendingPO  = "PENDING_PO"  // PO not yet available
	ClassPendingGRN = "PENDING_GRN" // GRN not yet available
	ClassAmbiguous  = "AMBIGUOUS"   // multiple PO candidates / unconfirmed candidate
	ClassBlocking   = "BLOCKING"    // quantity, price, tax or arithmetic mismatch
	ClassLowConf    = "LOW_CONFIDENCE"
	ClassWarn       = "WARN"
)

// DuplicateInfo is gathered from the database for duplicate checks.
type DuplicateInfo struct {
	SameFileInvoiceNumber string // an earlier invoice extracted from an identical file
	SameFileInvoiceID     string
	BusinessKeyInvoiceID  string // same seller GSTIN + invoice number + FY
	BusinessKeyNumber     string
	BusinessKeyStatus     string
	SimilarInvoiceIDs     []string // same vendor + amount + date, different number
	IsRevision            bool
}
