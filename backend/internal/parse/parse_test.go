package parse

import (
	"strings"
	"testing"

	"invoiceproc/internal/extract"
)

func lines(s string) []extract.Line {
	var out []extract.Line
	for _, l := range strings.Split(strings.Trim(s, "\n"), "\n") {
		out = append(out, extract.Line{Page: 1, Text: l})
	}
	return out
}

// Tally-style layout: stacked labels, quantity with unit, invoice-level CGST/SGST only.
const tally = `
Bharat Electricals & Controls                                                                INVOICE
No. 45, 2nd Cross, Rajajinagar, Bengaluru 560010
GSTIN/UIN: 29AABCB5521K1ZN
                                                                 Bill No.                    BEC/26-27/778
Buyer (Bill to)
                                                                 Dated                       20-Oct-2026
Inno Manufacturing Ltd
                                                                 Buyer's Order No.           PO-1002
GSTIN/UIN: 29AAACI1234F1ZY
Sl     Description of Goods                        HSN/SAC    Quantity      Rate       per          Amount
1      MCB-32A MCB Double Pole 32A                 85362030   40 Nos        525.00     Nos          21,000.00
2      CBL-4SQ Copper Cable 4 sq mm (90m)          85444999   10 Roll       6,400.00   Roll         64,000.00
                                                              CGST @ 9%                             7,650.00
                                                              SGST @ 9%                             7,650.00
                                                              Total                                 1,00,300.00
Amount Chargeable (in words): INR as above Only`

func TestTallyLayout(t *testing.T) {
	d := Parse(lines(tally), "INVOICE", Options{BuyerGSTINs: []string{"29AAACI1234F1ZY"}})
	want := map[string]string{"invoice_number": "BEC/26-27/778", "invoice_date": "2026-10-20", "po_reference": "PO-1002",
		"seller_gstin": "29AABCB5521K1ZN", "buyer_gstin": "29AAACI1234F1ZY", "grand_total": "100300", "cgst": "7650", "taxable_amount": "85000",
		"buyer_name": "Inno Manufacturing Ltd"}
	for k, v := range want {
		if f := d.Fields[k]; f == nil || f.Value != v {
			t.Errorf("%s = %+v, want %s", k, f, v)
		}
	}
	if len(d.Items) != 2 {
		t.Fatalf("items = %d", len(d.Items))
	}
	it := d.Items[1]
	if it.ItemCode != "CBL-4SQ" || *it.Quantity != 10 || *it.UnitPrice != 6400 || *it.TaxRate != 18 {
		t.Errorf("item 2 = %+v", it)
	}
	if OverallConfidence(d) < 0.75 {
		t.Errorf("confidence too low: %v", OverallConfidence(d))
	}
}

// OCR output with irregular spacing and a spaced-out title.
const ocr = `
                                                      TAX     INVOICE
       Acme    Industrial  Supplies   Pvt  Ltd                               Invoice No: INV-2026-0042
       Plot 12, MIDC Bhosari, Pune, Maharashtra 411026                       Invoice Date: 15/10/2026
       GSTIN:  27AAPFU0939F1ZV                                               PO  No: PO-1001
       Bill To:
       Inno Manufacturing Ltd
       GSTIN:  29AAACI1234F1ZY
       S.No  Item Code     Description                     HSN       Qty    Unit  Rate       Taxable Value GST%   Tax Amt    Total
       1     BRG-6205      Deep Groove Ball Bearing 6205   84821011  100    Nos   245.50     24,550.00    18%     4,419.00   28,969.00
       2    VBL-B42        V-Belt Classical B42            40103190  50     Nos   312.00     15,600.00    18%     2,808.00   18,408.00
                                                                                   Taxable Value                40,150.00
                                                                                   IGST                         7,227.00
                                                                                   Grand   Total                47,377.00`

func TestOCRLayout(t *testing.T) {
	d := Parse(lines(ocr), "INVOICE", Options{})
	if f := d.Fields["seller_name"]; f == nil || !strings.HasPrefix(f.Value, "Acme") {
		t.Errorf("seller_name = %+v", f)
	}
	if f := d.Fields["grand_total"]; f == nil || f.Value != "47377" || f.Confidence < 0.95 {
		t.Errorf("grand_total = %+v (arithmetic cross-check should boost confidence)", f)
	}
	if len(d.Items) != 2 || *d.Items[0].Quantity != 100 || d.Items[0].ItemCode != "BRG-6205" {
		t.Errorf("items = %+v", d.Items)
	}
}

func TestParseValues(t *testing.T) {
	for in, want := range map[string]float64{"1,00,300.00": 100300, "₹ 1,234.50": 1234.5, "(12.00)": -12, "18%": 18} {
		if got, ok := ParseNumber(in); !ok || got != want {
			t.Errorf("ParseNumber(%q) = %v", in, got)
		}
	}
	for in, want := range map[string]string{"15/10/2026": "2026-10-15", "20-Oct-2026": "2026-10-20", "2026-01-05": "2026-01-05", "Oct 3, 2026": "2026-10-03"} {
		if got, ok := ParseDate(in); !ok || got.Format("2006-01-02") != want {
			t.Errorf("ParseDate(%q) = %v", in, got)
		}
	}
}

// OCR of a handwriting-font invoice: the rupee sign comes out as "2", "%", "=" in front of amounts.
const ocrRupee = `
                TAX     INVOICE
                    Invoice Number:   GST-2023-00004
                   #     Description                Unit      Qty          Unit Price            Total                 Tax (IGST)              Total
                   1      Training - Installation    PCS      137              20.01            21.37             IGST 18% = %0.25             21.62
                   2     Belts - Timing, Poly-V,    PCS       164             T0.01             21.64            IGST 18% = 20.30              21.94
                                                                                            Sub-Total                                                 23.01
                                                                                           IGST                                                        20.55
                                                                                            Total Tax                                                  20.55
                                                                                            GRAND    TOTAL                                           23.56`

func TestOCRRupeeGlyph(t *testing.T) {
	ls := lines(ocrRupee)
	for i := range ls {
		ls[i].Conf = 80
	}
	d := Parse(ls, "INVOICE", Options{})
	if len(d.Items) != 2 {
		t.Fatalf("items = %d", len(d.Items))
	}
	for i, want := range []float64{1.37, 1.64} {
		it := d.Items[i]
		if it.UnitPrice == nil || *it.UnitPrice != 0.01 || it.TaxableValue == nil || *it.TaxableValue != want || it.TaxRate == nil || *it.TaxRate != 18 {
			t.Errorf("item %d: price=%v taxable=%v rate=%v desc=%q", i+1, f(it.UnitPrice), f(it.TaxableValue), f(it.TaxRate), it.Description)
		}
		if strings.Contains(it.Description, "IGST") {
			t.Errorf("tax cell leaked into description: %q", it.Description)
		}
	}
	want := map[string]string{"taxable_amount": "3.01", "total_tax": "0.55", "grand_total": "3.56"}
	for k, v := range want {
		if f := d.Fields[k]; f == nil || f.Value != v {
			t.Errorf("%s = %+v, want %s", k, f, v)
		}
	}
}

func f(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// GRN with a 3-line header, vertically centred multi-line descriptions, a received quantity printed
// above its row (pushed up by a warning icon), and a remarks column that must not leak into descriptions.
const grnCentred = `
GOODS RECEIPT NOTE                                                                                     PO Ref: PUR-2024-00004
GRN No: GRN/2526/000004 | Date: 11-Nov-2026
  SUPPLIER (CONSIGNOR)                                                         BUYER / RECEIVER
  Randhawa, Srinivas and Dubey                                                 Mitter Inc
 ITEM-WISE RECEIPT DETAILS
        Item                                                             Ord         Rcvd                               Rejection
 #                            Description            HSN      Unit                            Accepted     Rejected                            Remarks
        Code                                                             Qty         Qty                                 Reason
                   Training - Installation and
  1   ITM0001                                      999294     PCS       137.00       137.00       137.00           0   —                 —
                   Maintenance Training
                   Gaskets - Metal Lipped, Inner                                                                                         Short delivery;
                                                                                      94.71
  2   ITM0003      Diameter 140mm, Outer           40139090   PCS       125.00                     94.71           0   —                 balance 30 PCS
                                                                                         ⚠
                   Diameter 300mm                                                                                                        pending
  3   ITM0004      Couplings - Disc Type           73079990   PCS       187.00       187.00       187.00           0   —                 —
                                                                                                     Total Ordered:                                449.00`

func TestGRNCentredRows(t *testing.T) {
	d := Parse(lines(grnCentred), "GRN", Options{})
	if len(d.Items) != 3 {
		t.Fatalf("items = %d: %+v", len(d.Items), d.Items)
	}
	want := []struct {
		code, desc string
		ord, rcv   float64
	}{
		{"ITM0001", "Training - Installation and Maintenance Training", 137, 137},
		{"ITM0003", "Gaskets - Metal Lipped, Inner Diameter 140mm, Outer Diameter 300mm", 125, 94.71},
		{"ITM0004", "Couplings - Disc Type", 187, 187},
	}
	for i, w := range want {
		it := d.Items[i]
		if it.ItemCode != w.code || it.Description != w.desc || f(it.Quantity) != w.ord || f(it.QuantityReceived) != w.rcv || it.Unit != "PCS" {
			t.Errorf("row %d: code=%q desc=%q ord=%v rcv=%v unit=%q", i+1, it.ItemCode, it.Description, f(it.Quantity), f(it.QuantityReceived), it.Unit)
		}
	}
	if v := d.Fields["vendor_name"]; v == nil || v.Value != "Randhawa, Srinivas and Dubey" {
		t.Errorf("vendor_name = %+v", v)
	}
	if v := d.Fields["grn_number"]; v == nil || v.Value != "GRN/2526/000004" || v.Confidence < 0.75 {
		t.Errorf("grn_number = %+v", v)
	}
}
