package parse

import (
	"testing"

	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
)

func sp(s string) *string   { return &s }
func fp(f float64) *float64 { return &f }

func modelInvoice() *extract.VLMDoc {
	return &extract.VLMDoc{
		DocumentType: "INVOICE", DocumentNumber: sp("INV-2026-0042"), DocumentDate: sp("2026-10-15"), POReference: sp("PO-1001"),
		Seller: extract.VLMParty{Name: sp("Acme Industrial Supplies Pvt Ltd"), GSTIN: sp("27AAPFU0939F1ZV")},
		Buyer:  extract.VLMParty{Name: sp("Inno Manufacturing Ltd"), GSTIN: sp("29AAACI1234F1ZY")},
		Items: []extract.VLMItem{
			{ItemCode: sp("BRG-6205"), Description: sp("Deep Groove Ball Bearing"), Quantity: fp(100), UnitPrice: fp(245.5), TaxableValue: fp(24550), TaxRate: fp(18), Page: 1},
			{ItemCode: sp("VBL-B42"), Description: sp("V-Belt"), Quantity: fp(50), UnitPrice: fp(312), TaxableValue: fp(15600), TaxRate: fp(18), Page: 1},
		},
		TaxableAmount: fp(40150), IGST: fp(7227), TotalTax: fp(7227), GrandTotal: fp(47377),
		FieldPage: map[string]int{"document_number": 1, "document_date": 1, "po_reference": 1, "seller_gstin": 1, "buyer_gstin": 1,
			"taxable_amount": 1, "igst": 1, "total_tax": 1, "grand_total": 1},
		Pages: 1, ModelName: "test",
	}
}

func textLayer() []extract.Line {
	return lines(`
TAX INVOICE
Acme Industrial Supplies Pvt Ltd        Invoice No: INV-2026-0042
GSTIN: 27AAPFU0939F1ZV                  Invoice Date: 15/10/2026     PO No: PO-1001
Bill To: Inno Manufacturing Ltd  GSTIN: 29AAACI1234F1ZY
1  BRG-6205  Deep Groove Ball Bearing  100  Nos  245.50  24,550.00  18%
2  VBL-B42   V-Belt                     50  Nos  312.00  15,600.00  18%
Taxable Value 40,150.00   IGST 7,227.00   Grand Total 47,377.00`)
}

func TestFromVLMVerifiedByTextLayer(t *testing.T) {
	d := FromVLM(modelInvoice(), "INVOICE", textLayer(), Options{})
	for _, k := range []string{"invoice_number", "invoice_date", "po_reference", "seller_gstin", "grand_total"} {
		if f := d.Fields[k]; f == nil || f.Confidence < 0.95 {
			t.Errorf("%s should be verified by the text layer: %+v", k, f)
		}
	}
	if len(d.Items) != 2 || d.Items[0].Confidence < 0.9 || *d.Items[0].TaxAmount != 4419 {
		t.Errorf("items: %+v", d.Items)
	}
	if LowConfidence(d, 0.75) != nil {
		t.Errorf("nothing should need review: %v", LowConfidence(d, 0.75))
	}
}

// The model misreads the invoice number and one quantity: the text layer and arithmetic catch both.
func TestFromVLMCatchesMisreads(t *testing.T) {
	v := modelInvoice()
	v.DocumentNumber = sp("INV-2026-0048")
	v.Items[0].Quantity = fp(108)
	d := FromVLM(v, "INVOICE", textLayer(), Options{})
	if d.Fields["invoice_number"].Confidence >= 0.75 {
		t.Errorf("a number not printed in the document must be low confidence: %+v", d.Fields["invoice_number"])
	}
	if d.Items[0].Confidence >= 0.75 {
		t.Errorf("108 × 245.50 ≠ 24,550: line must be low confidence: %+v", d.Items[0])
	}
	low := LowConfidence(d, 0.75)
	if len(low) < 2 {
		t.Errorf("both misreads should go to review: %v", low)
	}
}

// Scans (no text layer): GSTIN snapped to the vendor master, parties swapped back by master data.
func TestFromVLMScanMasterData(t *testing.T) {
	defer gstin.SetChecksumMode("strict")
	v := modelInvoice()
	v.Seller, v.Buyer = v.Buyer, v.Seller // model mixed up the parties
	v.Buyer.GSTIN = sp("27AAPFU0939F1ZY") // and misread one character of the vendor GSTIN
	opt := Options{VendorGSTINs: []string{"27AAPFU0939F1ZV"}, BuyerGSTINs: []string{"29AAACI1234F1ZY"}}
	d := FromVLM(v, "INVOICE", nil, opt)
	if f := d.Fields["seller_gstin"]; f == nil || f.Value != "27AAPFU0939F1ZV" {
		t.Fatalf("seller GSTIN should be the snapped vendor GSTIN: %+v", f)
	}
	if f := d.Fields["seller_name"]; f == nil || f.Value != "Acme Industrial Supplies Pvt Ltd" {
		t.Errorf("seller name should follow the swap: %+v", f)
	}
	if f := d.Fields["invoice_number"]; f.Confidence != 0.85 {
		t.Errorf("unverifiable scan values are one reading (0.85): %+v", f)
	}
}

func TestFromVLMGRN(t *testing.T) {
	v := &extract.VLMDoc{DocumentType: "GRN", DocumentNumber: sp("GRN/2526/000004"), POReference: sp("PUR-2024-00004"),
		Items:     []extract.VLMItem{{ItemCode: sp("ITM0003"), Description: sp("Gaskets"), Quantity: fp(125), QtyReceived: fp(94.71), QtyAccepted: fp(94.71), QtyRejected: fp(0), Page: 1}},
		FieldPage: map[string]int{}, Pages: 1}
	d := FromVLM(v, "GRN", nil, Options{})
	it := d.Items[0]
	if *it.Quantity != 125 || *it.QuantityReceived != 94.71 || *it.QuantityAccepted != 94.71 || it.Confidence < 0.85 {
		t.Fatalf("GRN item: %+v", it)
	}
	if d.Fields["grn_number"].Value != "GRN/2526/000004" || d.Fields["po_reference"].Value != "PUR-2024-00004" {
		t.Fatalf("GRN header: %+v", d.Fields)
	}
}
