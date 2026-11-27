package service

import (
	"bytes"
	"errors"
	"invoiceproc/internal/utils"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Real ERP exports use their own column names; one row per line, header repeated.
func TestCSVWithAliases(t *testing.T) {
	csv := "PO No,Supplier GSTIN,PO Date,Sl No,Item Code,Item Description,HSN,Qty,UOM,Rate,GST %\n" +
		"PO-1001,27AAPFU0939F1ZV,15/11/2022,1,BRG-6205,Bearing 6205,84821011,100,Nos,\"1,245.50\",18\n" +
		"PO-1001,27AAPFU0939F1ZV,15/11/2022,2,VBL-B42,V-Belt,40103190,50,Nos,312,18%\n" +
		"PO-1002,27AAPFU0939F1ZV,2022-11-20,1,GRS-LT2,Grease,34039900,20,Kg,410,18\n"
	in, err := ParseCSV("purchase_orders", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.PurchaseOrders) != 2 || len(in.PurchaseOrders[0].Items) != 2 {
		t.Fatalf("grouping by PO failed: %+v", in.PurchaseOrders)
	}
	po := in.PurchaseOrders[0]
	it := po.Items[0]
	if po.PODate != "2022-11-15" || po.VendorGSTIN != "27AAPFU0939F1ZV" || it.UnitPrice != 1245.5 || it.QuantityOrdered != 100 ||
		it.TaxRate != 18 || it.UnitOfMeasure != "Nos" || it.ItemCode != "BRG-6205" || po.Items[1].TaxRate != 18 {
		t.Fatalf("mapping failed: %+v %+v", po, it)
	}
}

func TestCSVRowErrorsSaveNothing(t *testing.T) {
	csv := "grn_number,po_number,quantity_received,quantity_accepted,quantity_rejected\n" +
		"GRN-1,PO-1,100,90,10\n" +
		"GRN-1,PO-1,abc,0,0\n" +
		"GRN-2,PO-1,10,8,5\n"
	_, err := ParseCSV("goods_receipt_notes", strings.NewReader(csv))
	if err == nil || !errors.Is(err, utils.ErrInvalid) {
		t.Fatalf("expected a validation error, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "row 3: quantity_received \"abc\"") || !strings.Contains(msg, "row 4: accepted + rejected (13) exceeds received (10)") {
		t.Fatalf("row-numbered problems expected:\n%s", msg)
	}
}

func TestCSVMissingColumn(t *testing.T) {
	_, err := ParseCSV("purchase_orders", strings.NewReader("PO No,Qty\nPO-1,5\n"))
	if err == nil || !strings.Contains(err.Error(), "missing required column(s) vendor_gstin, unit_price") {
		t.Fatalf("got %v", err)
	}
}

// The downloadable template must import cleanly, including Excel date cells.
func TestWorkbookRoundTrip(t *testing.T) {
	b, err := TemplateXLSX()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	// replace the GRN example date with a real Excel date cell
	f.SetCellFormula("Goods Receipt Notes", "D2", "")
	f.SetCellValue("Goods Receipt Notes", "D2", 44880) // 2022-11-15 as an Excel serial
	var buf bytes.Buffer
	f.Write(&buf)
	in, err := ParseWorkbook(&buf, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Vendors) != 1 || len(in.PurchaseOrders) != 1 || len(in.GoodsReceiptNotes) != 1 {
		t.Fatalf("all three sheets should import: %+v", in)
	}
	g := in.GoodsReceiptNotes[0]
	if g.ReceiptDate != "2022-11-15" || g.Items[0].QuantityAccepted != 90 || g.Items[0].QuantityRejected != 10 {
		t.Fatalf("GRN from workbook: %+v", g)
	}
	if in.PurchaseOrders[0].Items[0].UnitPrice != 245.5 || in.Vendors[0].GSTIN != "27AAPFU0939F1ZV" || !in.Vendors[0].IsApproved {
		t.Fatalf("PO/vendor from workbook: %+v %+v", in.PurchaseOrders[0], in.Vendors[0])
	}
}

func TestSingleSheetWorkbookUsesKind(t *testing.T) {
	f := excelize.NewFile()
	f.SetSheetRow("Sheet1", "A1", &[]any{"Vendor Name", "GST No", "Approved"})
	f.SetSheetRow("Sheet1", "A2", &[]any{"Bharat Electricals", "29AABCB5521K1ZN", "no"})
	var buf bytes.Buffer
	f.Write(&buf)
	in, err := ParseWorkbook(&buf, "vendors")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Vendors) != 1 || in.Vendors[0].LegalName != "Bharat Electricals" || in.Vendors[0].IsApproved {
		t.Fatalf("%+v", in.Vendors)
	}
}
