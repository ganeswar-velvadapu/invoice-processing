package decision

import (
	"testing"

	"invoiceproc/internal/model"
)

func TestPrecedence(t *testing.T) {
	cs := []model.Check{
		{Code: "PO_MATCH", Status: model.Pass},
		{Code: "GRN_ACCEPTED_QUANTITY", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "QUANTITY_EXCEEDS_GRN", Reason: "qty"},
		{Code: "EXTRACTION_CONFIDENCE", Status: model.Warning, Class: model.ClassLowConf, ExceptionCode: "LOW_EXTRACTION_CONFIDENCE"},
	}
	r := Decide("INV-1", []string{"PO-1"}, cs, nil, 1)
	if r.Decision != Hold || r.InvoiceStatus != "ON_HOLD" {
		t.Fatalf("want HOLD got %s", r.Decision)
	}
	r = Decide("INV-1", nil, cs, map[string]bool{"QUANTITY_EXCEEDS_GRN": true}, 1)
	if r.Decision != Review {
		t.Fatalf("waived blocking should fall to REVIEW, got %s", r.Decision)
	}
	cs = append(cs, model.Check{Code: "DUPLICATE_INVOICE", Status: model.Fail, Class: model.ClassDuplicate, ExceptionCode: "DUPLICATE_INVOICE"})
	if r = Decide("INV-1", nil, cs, nil, 1); r.Decision != Hold || r.DecidingClass != model.ClassDuplicate {
		t.Fatalf("duplicate must win: %+v", r)
	}
	if r = Decide("INV-1", nil, []model.Check{{Code: "X", Status: model.Pass}}, nil, 1); r.Decision != Approve {
		t.Fatal("expected approve")
	}
	pend := []model.Check{{Code: "GRN_ACCEPTED_QUANTITY", Status: model.Fail, Class: model.ClassPendingGRN, ExceptionCode: "GRN_NOT_RECEIVED"},
		{Code: "PRICE_MATCH", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "PRICE_MISMATCH"}}
	if r = Decide("INV-1", nil, pend, nil, 1); r.Decision != PendingDocuments || r.InvoiceStatus != "PENDING_GRN" {
		t.Fatalf("pending GRN expected: %+v", r)
	}
}
