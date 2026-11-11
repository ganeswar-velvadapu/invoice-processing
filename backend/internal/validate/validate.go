// Package validate runs deterministic invoice-level checks: required fields, formats, vendor
// identity, arithmetic, tax structure, duplicates and extraction confidence.
package validate

import (
	"fmt"
	"math"
	"strings"
	"time"

	"invoiceproc/internal/gstin"
	"invoiceproc/internal/model"
	"invoiceproc/internal/util"
)

type Input struct {
	Invoice             *model.Invoice
	Vendor              *model.Vendor // resolved by seller GSTIN, nil if unknown
	VendorByNameOnly    *model.Vendor // a vendor whose name matches but GSTIN differs
	Duplicates          model.DuplicateInfo
	Tolerance           model.Tolerance
	BuyerGSTINs         []string
	SupportedCurrencies []string
	Now                 time.Time
}

func money(f float64) string { return fmt.Sprintf("%.2f", f) }

func Run(in Input) []model.Check {
	inv := in.Invoice
	var cs []model.Check
	add := func(c model.Check) { cs = append(cs, c) }
	tol := in.Tolerance
	taxTol := tol.TaxRoundingAbs

	// REQUIRED_FIELDS
	var missing []string
	if inv.InvoiceNumber == "" {
		missing = append(missing, "invoice_number")
	}
	if inv.InvoiceDate == nil {
		missing = append(missing, "invoice_date")
	}
	if inv.SellerGSTIN == "" {
		missing = append(missing, "seller_gstin")
	}
	if inv.GrandTotal == nil {
		missing = append(missing, "grand_total")
	}
	if len(inv.Items) == 0 {
		missing = append(missing, "line_items")
	}
	if len(missing) > 0 {
		add(model.Check{Code: "REQUIRED_FIELDS", Status: model.Fail, Class: model.ClassMissing, ExceptionCode: "MISSING_REQUIRED_FIELDS",
			Expected: "invoice_number, invoice_date, seller_gstin, grand_total, line_items", Actual: "missing: " + strings.Join(missing, ", "),
			Reason: "Mandatory invoice information is missing: " + strings.Join(missing, ", ") + "."})
	} else {
		add(model.Check{Code: "REQUIRED_FIELDS", Status: model.Pass, Reason: "All mandatory header fields and at least one line item are present."})
	}

	// INVOICE_DATE_VALID
	if inv.InvoiceDate != nil {
		d := *inv.InvoiceDate
		switch {
		case d.After(in.Now.AddDate(0, 0, 1+tol.DateToleranceDays)):
			add(model.Check{Code: "INVOICE_DATE_VALID", Status: model.Fail, Class: model.ClassBlocking, Actual: d.Format("2006-01-02"),
				Reason: "Invoice date is in the future."})
		case d.Before(in.Now.AddDate(-1, 0, 0)):
			add(model.Check{Code: "INVOICE_DATE_VALID", Status: model.Warning, Class: model.ClassWarn, Actual: d.Format("2006-01-02"),
				Reason: "Invoice is more than one year old; confirm it is still payable."})
		case inv.DueDate != nil && inv.DueDate.Before(d):
			add(model.Check{Code: "INVOICE_DATE_VALID", Status: model.Warning, Class: model.ClassWarn, Actual: inv.DueDate.Format("2006-01-02"),
				Reason: "Due date is before the invoice date."})
		default:
			add(model.Check{Code: "INVOICE_DATE_VALID", Status: model.Pass, Actual: d.Format("2006-01-02"), Reason: "Invoice date is plausible."})
		}
	}

	// GSTIN_FORMAT (checksum policy: see gstin.SetChecksumMode)
	if inv.SellerGSTIN != "" {
		r, ignored := gstin.Check(inv.SellerGSTIN)
		switch {
		case !r.Valid:
			add(model.Check{Code: "GSTIN_FORMAT", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "INVALID_SELLER_GSTIN",
				Actual: inv.SellerGSTIN, Reason: "Supplier GSTIN is invalid: " + r.Reason + "."})
		case ignored && gstin.ChecksumMode() == "warn":
			add(model.Check{Code: "GSTIN_FORMAT", Status: model.Warning, Class: model.ClassWarn, ExceptionCode: "GSTIN_CHECKSUM_MISMATCH",
				Actual: inv.SellerGSTIN, Reason: "Supplier GSTIN format is valid but its checksum digit does not match; confirm the GSTIN."})
		case ignored:
			add(model.Check{Code: "GSTIN_FORMAT", Status: model.Pass, Actual: inv.SellerGSTIN,
				Reason: "Supplier GSTIN format is valid (checksum not enforced: GSTIN_CHECKSUM=off)."})
		default:
			add(model.Check{Code: "GSTIN_FORMAT", Status: model.Pass, Actual: inv.SellerGSTIN,
				Reason: "Supplier GSTIN format and checksum are valid (registration status is not verified)."})
		}
	}
	if inv.BuyerGSTIN != "" {
		r, ignored := gstin.Check(inv.BuyerGSTIN)
		ours := len(in.BuyerGSTINs) == 0
		for _, g := range in.BuyerGSTINs {
			if gstin.Normalize(g) == inv.BuyerGSTIN {
				ours = true
			}
		}
		switch {
		case !r.Valid:
			add(model.Check{Code: "BUYER_GSTIN", Status: model.Warning, Class: model.ClassWarn, ExceptionCode: "INVALID_BUYER_GSTIN",
				Actual: inv.BuyerGSTIN, Reason: "Buyer GSTIN is invalid: " + r.Reason + "; input tax credit may be at risk."})
		case ignored && gstin.ChecksumMode() == "warn":
			add(model.Check{Code: "BUYER_GSTIN", Status: model.Warning, Class: model.ClassWarn, ExceptionCode: "INVALID_BUYER_GSTIN",
				Actual: inv.BuyerGSTIN, Reason: "Buyer GSTIN checksum digit does not match; confirm the GSTIN."})
		case !ours:
			add(model.Check{Code: "BUYER_GSTIN", Status: model.Warning, Class: model.ClassWarn, ExceptionCode: "BUYER_GSTIN_NOT_OURS",
				Expected: strings.Join(in.BuyerGSTINs, ", "), Actual: inv.BuyerGSTIN, Reason: "Invoice is billed to a GSTIN that is not one of our registrations."})
		default:
			add(model.Check{Code: "BUYER_GSTIN", Status: model.Pass, Actual: inv.BuyerGSTIN, Reason: "Buyer GSTIN is valid and belongs to us."})
		}
	}

	// VENDOR_APPROVED
	switch v := in.Vendor; {
	case v == nil && in.VendorByNameOnly != nil:
		add(model.Check{Code: "VENDOR_APPROVED", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "VENDOR_IDENTITY_MISMATCH",
			Expected: in.VendorByNameOnly.GSTIN, Actual: inv.SellerGSTIN,
			Reason: fmt.Sprintf("Supplier name resembles vendor %q but the GSTIN on the invoice does not match the vendor record.", in.VendorByNameOnly.LegalName)})
	case v == nil:
		add(model.Check{Code: "VENDOR_APPROVED", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "VENDOR_NOT_FOUND",
			Actual: inv.SellerGSTIN, Reason: "No vendor master record exists for the supplier GSTIN."})
	case v.Status == "BLOCKED":
		add(model.Check{Code: "VENDOR_APPROVED", Status: model.Fail, Class: model.ClassReject, ExceptionCode: "VENDOR_BLOCKED",
			Actual: v.LegalName, Reason: "Vendor is blocked; invoices from this vendor are not eligible for payment."})
	case !v.IsApproved || v.Status != "ACTIVE":
		add(model.Check{Code: "VENDOR_APPROVED", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "VENDOR_NOT_APPROVED",
			Actual: v.LegalName, Reason: "Vendor exists but is not approved/active."})
	default:
		reason := "Supplier GSTIN matches approved vendor " + v.LegalName + "."
		st := model.Pass
		cls := ""
		if inv.SellerName != "" && util.TextSimilarity(inv.SellerName, v.LegalName) < 0.3 {
			st, cls = model.Warning, model.ClassWarn
			reason = fmt.Sprintf("Supplier GSTIN matches vendor %q but the printed name %q differs.", v.LegalName, inv.SellerName)
		}
		add(model.Check{Code: "VENDOR_APPROVED", Status: st, Class: cls, ExceptionCode: "VENDOR_NAME_MISMATCH", Expected: v.GSTIN, Actual: inv.SellerGSTIN, Reason: reason})
	}

	// CURRENCY_SUPPORTED
	cur := inv.Currency
	if cur == "" {
		cur = "INR"
	}
	supported := false
	for _, c := range in.SupportedCurrencies {
		if c == cur {
			supported = true
		}
	}
	if supported {
		add(model.Check{Code: "CURRENCY_SUPPORTED", Status: model.Pass, Actual: cur, Reason: "Currency " + cur + " is supported."})
	} else {
		add(model.Check{Code: "CURRENCY_SUPPORTED", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "UNSUPPORTED_CURRENCY",
			Actual: cur, Expected: strings.Join(in.SupportedCurrencies, ", "), Reason: "Currency " + cur + " is not supported."})
	}

	// LINE_VALUES_VALID + LINE_ARITHMETIC
	var badVals, badMath []string
	var sumTaxable, sumTax float64
	allTaxable, allTax := true, true
	for _, it := range inv.Items {
		ln := fmt.Sprintf("line %d", it.LineNumber)
		if it.Quantity == nil || *it.Quantity <= 0 || it.UnitPrice == nil || *it.UnitPrice < 0 {
			badVals = append(badVals, ln)
			continue
		}
		if it.TaxableValue != nil {
			exp := *it.Quantity**it.UnitPrice - it.Discount
			if math.Abs(exp-*it.TaxableValue) > taxTol {
				badMath = append(badMath, fmt.Sprintf("%s: %s × %s − %s = %s, invoice shows %s", ln,
					util.FormatQty(*it.Quantity), money(*it.UnitPrice), money(it.Discount), money(exp), money(*it.TaxableValue)))
			}
			sumTaxable += *it.TaxableValue
		} else {
			allTaxable = false
		}
		if it.TaxAmount != nil {
			sumTax += *it.TaxAmount
		} else {
			allTax = false
		}
	}
	if len(badVals) > 0 {
		add(model.Check{Code: "LINE_VALUES_VALID", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "INVALID_LINE_VALUES",
			Actual: strings.Join(badVals, ", "), Reason: "Quantity or unit price is missing, zero or negative on " + strings.Join(badVals, ", ") + "."})
	} else if len(inv.Items) > 0 {
		add(model.Check{Code: "LINE_VALUES_VALID", Status: model.Pass, Reason: "All line quantities and prices are positive."})
	}
	if len(badMath) > 0 {
		add(model.Check{Code: "LINE_ARITHMETIC", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "LINE_ARITHMETIC_ERROR",
			Reason: "Line amounts do not equal quantity × price − discount: " + strings.Join(badMath, "; ") + "."})
	} else if len(inv.Items) > 0 && len(badVals) == 0 {
		add(model.Check{Code: "LINE_ARITHMETIC", Status: model.Pass, Reason: "Every line's taxable value equals quantity × unit price − discount."})
	}

	// TAX_ARITHMETIC: structure (intra vs inter state) and totals
	cg, sg, ig := util.F(inv.CGST), util.F(inv.SGST), util.F(inv.IGST)
	var taxIssues []string
	sellerState, buyerState := stateOf(inv.SellerGSTIN), stateOf(inv.BuyerGSTIN)
	if sellerState != "" && buyerState != "" {
		if sellerState == buyerState {
			if ig > taxTol {
				taxIssues = append(taxIssues, fmt.Sprintf("intra-state supply (state %s) should charge CGST+SGST, but IGST %s is charged", sellerState, money(ig)))
			}
			if math.Abs(cg-sg) > taxTol {
				taxIssues = append(taxIssues, fmt.Sprintf("CGST %s and SGST %s should be equal", money(cg), money(sg)))
			}
		} else if cg+sg > taxTol {
			taxIssues = append(taxIssues, fmt.Sprintf("inter-state supply (%s→%s) should charge IGST, but CGST/SGST %s is charged", sellerState, buyerState, money(cg+sg)))
		}
	}
	totalTax := util.F(inv.TotalTax)
	if inv.TotalTax != nil && cg+sg+ig > 0 && math.Abs(cg+sg+ig-totalTax) > taxTol {
		taxIssues = append(taxIssues, fmt.Sprintf("CGST+SGST+IGST = %s but total tax = %s", money(cg+sg+ig), money(totalTax)))
	}
	if allTax && len(inv.Items) > 0 && inv.TotalTax != nil && math.Abs(sumTax-totalTax) > taxTol*float64(max(1, len(inv.Items))) {
		taxIssues = append(taxIssues, fmt.Sprintf("sum of line tax %s differs from total tax %s", money(sumTax), money(totalTax)))
	}
	for _, it := range inv.Items {
		if it.TaxRate != nil && it.TaxableValue != nil && it.TaxAmount != nil {
			exp := *it.TaxableValue * *it.TaxRate / 100
			if math.Abs(exp-*it.TaxAmount) > taxTol {
				taxIssues = append(taxIssues, fmt.Sprintf("line %d tax %s ≠ %s × %g%% = %s", it.LineNumber, money(*it.TaxAmount), money(*it.TaxableValue), *it.TaxRate, money(exp)))
			}
		}
	}
	if len(taxIssues) > 0 {
		add(model.Check{Code: "TAX_ARITHMETIC", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "TAX_CALCULATION_ERROR",
			Reason: "GST does not compute: " + strings.Join(taxIssues, "; ") + "."})
	} else if inv.TotalTax != nil {
		add(model.Check{Code: "TAX_ARITHMETIC", Status: model.Pass, Actual: money(totalTax), Reason: "GST components and totals are consistent with the supply type."})
	}

	// INVOICE_ARITHMETIC — each failing comparison carries its own expected/actual values
	if inv.GrandTotal != nil {
		taxable := util.F(inv.TaxableAmount)
		if inv.TaxableAmount == nil && allTaxable {
			taxable = sumTaxable
		}
		exp := taxable + totalTax + util.F(inv.OtherCharges) + util.F(inv.RoundOff)
		type issue struct{ what, expected, actual string }
		var issues []issue
		if math.Abs(exp-*inv.GrandTotal) > taxTol {
			issues = append(issues, issue{fmt.Sprintf("taxable %s + tax %s + other %s + round-off %s = %s, but grand total is %s",
				money(taxable), money(totalTax), money(util.F(inv.OtherCharges)), money(util.F(inv.RoundOff)), money(exp), money(*inv.GrandTotal)),
				"grand total " + money(exp), "grand total " + money(*inv.GrandTotal)})
		}
		if allTaxable && len(inv.Items) > 0 && inv.TaxableAmount != nil && math.Abs(sumTaxable-*inv.TaxableAmount) > taxTol {
			issues = append(issues, issue{fmt.Sprintf("sum of line taxable values %s ≠ taxable total %s", money(sumTaxable), money(*inv.TaxableAmount)),
				"lines sum to " + money(*inv.TaxableAmount), "lines sum to " + money(sumTaxable)})
		}
		if math.Abs(util.F(inv.RoundOff)) > 1.0 {
			issues = append(issues, issue{fmt.Sprintf("round-off %s exceeds ₹1", money(util.F(inv.RoundOff))), "round-off ≤ 1.00", "round-off " + money(util.F(inv.RoundOff))})
		}
		if len(issues) > 0 {
			var whats []string
			for _, i := range issues {
				whats = append(whats, i.what)
			}
			add(model.Check{Code: "INVOICE_ARITHMETIC", Status: model.Fail, Class: model.ClassBlocking, ExceptionCode: "TOTAL_MISMATCH",
				Expected: issues[0].expected, Actual: issues[0].actual, Reason: "Invoice totals do not add up: " + strings.Join(whats, "; ") + "."})
		} else {
			add(model.Check{Code: "INVOICE_ARITHMETIC", Status: model.Pass, Expected: money(exp), Actual: money(*inv.GrandTotal),
				Reason: "Taxable value + tax + other charges + round-off equals the grand total."})
		}
	}

	// DUPLICATE_INVOICE
	dup := in.Duplicates
	switch {
	case dup.IsRevision:
		add(model.Check{Code: "DUPLICATE_INVOICE", Status: model.Pass, Reason: "Invoice is marked as an authorised revision of an earlier invoice."})
	case dup.SameFileInvoiceID != "":
		add(model.Check{Code: "DUPLICATE_INVOICE", Status: model.Fail, Class: model.ClassDuplicate, ExceptionCode: "DUPLICATE_INVOICE",
			Actual: dup.SameFileInvoiceNumber, Evidence: map[string]any{"duplicate_of_invoice_id": dup.SameFileInvoiceID, "basis": "identical file (SHA-256)"},
			Reason: "An identical file was already processed as invoice " + dup.SameFileInvoiceNumber + "."})
	case dup.BusinessKeyInvoiceID != "":
		add(model.Check{Code: "DUPLICATE_INVOICE", Status: model.Fail, Class: model.ClassDuplicate, ExceptionCode: "DUPLICATE_INVOICE",
			Actual: inv.InvoiceNumber, Evidence: map[string]any{"duplicate_of_invoice_id": dup.BusinessKeyInvoiceID, "existing_status": dup.BusinessKeyStatus,
				"basis": "same supplier GSTIN + invoice number + financial year"},
			Reason: fmt.Sprintf("Invoice %s from this supplier was already received in the same financial year (status %s).", dup.BusinessKeyNumber, dup.BusinessKeyStatus)})
	case len(dup.SimilarInvoiceIDs) > 0:
		add(model.Check{Code: "DUPLICATE_INVOICE", Status: model.Warning, Class: model.ClassWarn, ExceptionCode: "POSSIBLE_DUPLICATE",
			Evidence: map[string]any{"similar_invoice_ids": dup.SimilarInvoiceIDs, "basis": "same supplier, date and amount; different number"},
			Reason:   "Another invoice from this supplier has the same date and amount under a different number; confirm it is not a re-numbered duplicate."})
	default:
		add(model.Check{Code: "DUPLICATE_INVOICE", Status: model.Pass, Reason: "No previously received invoice with the same file, or supplier + number + financial year, was found."})
	}

	// EXTRACTION_CONFIDENCE
	if inv.ExtractionConfidence < tol.MinExtractionConfidence {
		add(model.Check{Code: "EXTRACTION_CONFIDENCE", Status: model.Warning, Class: model.ClassLowConf, ExceptionCode: "LOW_EXTRACTION_CONFIDENCE",
			Expected: fmt.Sprintf(">= %.2f", tol.MinExtractionConfidence), Actual: fmt.Sprintf("%.2f", inv.ExtractionConfidence),
			Reason: "Extraction confidence is below the configured threshold; verify the extracted values against the original document."})
	} else {
		add(model.Check{Code: "EXTRACTION_CONFIDENCE", Status: model.Pass, Actual: fmt.Sprintf("%.2f", inv.ExtractionConfidence),
			Reason: "Extraction confidence meets the configured threshold."})
	}
	return cs
}

func stateOf(g string) string {
	if len(g) == 15 {
		return g[:2]
	}
	return ""
}
