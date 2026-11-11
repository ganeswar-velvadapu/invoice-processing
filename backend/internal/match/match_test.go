package match

import (
	"testing"

	"invoiceproc/internal/model"
)

func f(v float64) *float64 { return &v }

func TestThreeWayQuantityExceedsGRN(t *testing.T) {
	inv := &model.Invoice{SellerGSTIN: "G", POReferences: []string{"PO-101"}, Items: []model.InvoiceItem{
		{InvoiceItemID: "ii1", LineNumber: 1, ItemCode: "X-1", Quantity: f(100), UnitPrice: f(10), TaxRate: f(18)}}}
	po := model.PurchaseOrder{PurchaseOrderID: "p1", PONumber: "PO-101", VendorID: "v1", Status: "APPROVED", GRNIDs: []string{"g1"},
		Items: []model.POItem{{POItemID: "pi1", LineNumber: 1, ItemCode: "X-1", QuantityOrdered: 100, UnitPrice: 10, TaxRate: 18, QuantityAccepted: 90, HasReceipt: true}}}
	tol := model.Tolerance{PricePct: 1, PriceAbs: 1, TaxRoundingAbs: 1}
	r := Run(Input{Invoice: inv, VendorID: "v1", ReferencedPOs: []model.PurchaseOrder{po}, Tolerance: tol})
	got := map[string]string{}
	for _, c := range r.Checks {
		got[c.Code] = c.Status
	}
	if got["PO_MATCH"] != model.Pass || got["QUANTITY_MATCH"] != model.Pass || got["PRICE_MATCH"] != model.Pass || got["GRN_ACCEPTED_QUANTITY"] != model.Fail {
		t.Fatalf("checks = %v", got)
	}
	// price within ±1% passes, beyond fails
	inv.Items[0].UnitPrice = f(10.05)
	inv.Items[0].Quantity = f(90)
	r = Run(Input{Invoice: inv, VendorID: "v1", ReferencedPOs: []model.PurchaseOrder{po}, Tolerance: tol})
	for _, c := range r.Checks {
		if c.Status != model.Pass {
			t.Fatalf("expected all pass, got %s %s: %s", c.Code, c.Status, c.Reason)
		}
	}
}

func TestCandidateSearchWithoutReference(t *testing.T) {
	inv := &model.Invoice{SellerGSTIN: "G", Items: []model.InvoiceItem{{InvoiceItemID: "a", LineNumber: 1, ItemCode: "X-1", Quantity: f(5), UnitPrice: f(10)}}}
	mk := func(id, num string) model.PurchaseOrder {
		return model.PurchaseOrder{PurchaseOrderID: id, PONumber: num, VendorID: "v1", Status: "APPROVED",
			Items: []model.POItem{{POItemID: id + "i", LineNumber: 1, ItemCode: "X-1", QuantityOrdered: 10, UnitPrice: 10}}}
	}
	r := Run(Input{Invoice: inv, VendorID: "v1", VendorPOs: []model.PurchaseOrder{mk("p1", "PO-1")}, RejectedPOIDs: map[string]bool{}})
	if r.Checks[0].Status != model.Pass || len(r.Allocations) != 1 {
		t.Fatalf("single strong candidate should match: %+v", r.Checks)
	}
	r = Run(Input{Invoice: inv, VendorID: "v1", VendorPOs: []model.PurchaseOrder{mk("p1", "PO-1"), mk("p2", "PO-2")}, RejectedPOIDs: map[string]bool{}})
	if r.Checks[0].Class != model.ClassAmbiguous {
		t.Fatalf("two equal candidates must go to review: %+v", r.Checks[0])
	}
	r = Run(Input{Invoice: inv, VendorID: "v1", RejectedPOIDs: map[string]bool{}})
	if r.Checks[0].ExceptionCode != "PO_NOT_FOUND" {
		t.Fatalf("no candidates must be PO_NOT_FOUND: %+v", r.Checks[0])
	}
}

func TestApproximateReference(t *testing.T) {
	inv := &model.Invoice{SellerGSTIN: "G", POReferences: []string{"QUR-2024-00004"}, Items: []model.InvoiceItem{
		{InvoiceItemID: "ii1", LineNumber: 1, Description: "Couplings - Disc Type", Quantity: f(187), UnitPrice: f(0.01)}}}
	po := model.PurchaseOrder{PurchaseOrderID: "p1", PONumber: "PUR-2024-00004", VendorID: "v1", Status: "APPROVED", GRNIDs: []string{"g"},
		Items: []model.POItem{{POItemID: "pi1", LineNumber: 4, Description: "Couplings - Disc Type", QuantityOrdered: 187, UnitPrice: 0.01, QuantityAccepted: 187, HasReceipt: true}}}
	r := Run(Input{Invoice: inv, VendorID: "v1", ReferencedPOs: []model.PurchaseOrder{po}, ApproxRefs: map[string]string{"p1": "QUR-2024-00004"},
		Tolerance: model.Tolerance{PricePct: 1, PriceAbs: 1}})
	if r.POMatches[0].Method != MethodRule || r.POMatches[0].Status != StatusConfirmed || r.Checks[0].Status != model.Pass {
		t.Fatalf("approximate reference should confirm with RULE_BASED: %+v %+v", r.POMatches, r.Checks[0])
	}
	if len(r.Allocations) != 1 {
		t.Fatalf("lines should still be allocated and checked: %+v", r.Allocations)
	}
}
