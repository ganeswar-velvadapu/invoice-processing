package classify

import (
	"strings"
	"testing"
)

func cls(t *testing.T, text string) Result {
	t.Helper()
	return Classify(strings.Split(strings.Trim(text, "\n"), "\n"))
}

func TestClassify(t *testing.T) {
	cases := []struct{ name, want, text string }{
		{"ocr misread title", "PO", `
                                                         Qurchase Order NG
                 QUCLHASE           OMDESW                    WZ-202    4-00004
                                                         Order Date: 16/11/2022
                     Miter   Tac                          Candhawe,   Srinivas  and Dubey`},
		{"eway bill with invoice references", "EWAY_BILL", `
  LORRY RECEIPT / E-WAY BILL | 992031320787
      FROM (CONSIGNOR)                                   TO (CONSIGNEE)
   Invoice No.                  GST-2023-00004           LR No.                     CN00004/557
   Invoice Date                 23/11/2022               LR Date                    11-Nov-2026
   PO Reference                 PUR-2024-00004           Mode                       Road
   Invoice Value                12.98                    Vehicle No.                GJ48HE3070
  #   Description of Goods   HSN  UOM  Qty  Taxable Value  GST%  CGST  SGST  IGST
                                                         Taxable Value      10.98
                                                         Total GST           2.00`},
		{"grn that mentions an invoice", "GRN", `
GOODS RECEIPT NOTE                                              PO: PO-2022-00005
GRN/2526/000005 | 11-Nov-2026                                   Invoice: TAX-2024-00005
  #   Item Code   Description   HSN   UOM   Ord   Rcvd   Acc.   Rej.`},
		{"invoice referencing a PO", "INVOICE", `
                TAK     INVOICE
                                                    PO Ref: PUR-2024-00004
                    VENDOR                          INVOICE DETAILS
                    Against Purchase Order: PUR-2024-00004
                    Taxable Value   100   Grand Total 118`},
		{"po referencing invoice terms", "PO", `
               PURCHASE            ORDER                          PO-2022-00005
                   BILLED PARTY                       FROM
                   Issue Date: 30/11/2023      Payment Terms: Net 90`},
		{"eway bill whose banner OCR missed", "EWAY_BILL", `
        PART A— Document Details
      Invoice/Doc No.      BILL-2023-00006                            Taxable Value       178,731.58
      Invoice Date         02/03/2023                                 Total GST           32,171.68
      Invoice Value        210,903.26                                 Total Cess         0.00
      PO Reference         REQ-2022-00006                             Ship-To GSTIN      28QQYXW5869P3Z9
      Supply Type         B2B
     Sub-Type             Import
      CONSIGNOR (FROM)
      Acharya and Sons                                                CONSIGNEE (TO)
     GOODSDETAILS (HSN-WISE)
                          Description           HSN Unit   Qty     Taxable Value GST%  CGST   SGST      IGST`},
		{"noise", "OTHER", `
[2E   SB      amma
a   ee       ee    a     paee
| | Shadlnpcoehdgatiaeninsiciion sascinnsnneanaitaes`},
	}
	for _, c := range cases {
		r := cls(t, c.text)
		if r.DocumentType != c.want {
			t.Errorf("%s: got %s (%.2f) scores=%v evidence=%v", c.name, r.DocumentType, r.Confidence, r.Scores, r.Evidence)
		}
	}
}

func TestFuzzyFind(t *testing.T) {
	if _, _, ok := FuzzyFind(Words("PURCHASE COKE"), "purchase order", 0.2); ok {
		t.Error("purchase coke must not match purchase order")
	}
	if _, _, ok := FuzzyFind(Words("Qurchase  Order"), "purchase order", 0.2); !ok {
		t.Error("qurchase order should match")
	}
	if _, _, ok := FuzzyFind(Words("TAX IN0ICE"), "tax invoice", 0.2); !ok {
		t.Error("digit-folded OCR word should match")
	}
}
