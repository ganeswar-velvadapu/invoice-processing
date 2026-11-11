// Package decision derives the final, explainable decision from persisted check results using an
// explicit precedence. It never looks at raw documents; only at checks (and waived exceptions).
package decision

import (
	"fmt"
	"strings"

	"invoiceproc/internal/model"
)

const (
	Approve          = "APPROVE"
	Review           = "REVIEW"
	Hold             = "HOLD"
	Reject           = "REJECT"
	PendingDocuments = "PENDING_DOCUMENTS"
)

// Precedence (rule version model.RuleVersion):
//  1. REJECTING  (e.g. blocked vendor)                → REJECT
//  2. DUPLICATE  (confirmed duplicate)                → HOLD  (policy: hold, never silently discard)
//  3. MISSING    (mandatory information missing)      → HOLD
//  4. PENDING_PO / PENDING_GRN                        → PENDING_DOCUMENTS
//  5. AMBIGUOUS  (multiple / unconfirmed PO match)    → REVIEW
//  6. BLOCKING   (qty, price, tax, arithmetic, vendor) → HOLD
//  7. LOW_CONFIDENCE or WARN                          → REVIEW
//  8. otherwise                                       → APPROVE
var precedence = []struct {
	class    string
	decision string
}{
	{model.ClassReject, Reject},
	{model.ClassDuplicate, Hold},
	{model.ClassMissing, Hold},
	{model.ClassPendingPO, PendingDocuments},
	{model.ClassPendingGRN, PendingDocuments},
	{model.ClassAmbiguous, Review},
	{model.ClassBlocking, Hold},
	{model.ClassLowConf, Review},
	{model.ClassWarn, Review},
}

type CheckView struct {
	Check    string `json:"check"`
	Status   string `json:"status"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Reason   string `json:"reason"`
	Waived   bool   `json:"waived,omitempty"`
}

type Result struct {
	InvoiceNumber       string      `json:"invoice_number"`
	MatchedPOs          []string    `json:"matched_purchase_orders"`
	Decision            string      `json:"decision"`
	InvoiceStatus       string      `json:"invoice_status"`
	Summary             string      `json:"summary"`
	DecidingClass       string      `json:"deciding_class,omitempty"`
	DecidingChecks      []string    `json:"deciding_checks,omitempty"`
	Checks              []CheckView `json:"checks"`
	RecommendedAction   string      `json:"recommended_action"`
	RequiresHumanReview bool        `json:"requires_human_review"`
	RuleVersion         string      `json:"rule_version"`
	ToleranceVersion    int         `json:"tolerance_version"`
}

// Decide applies precedence to non-passing checks. waived holds exception codes AP has waived;
// a waived check no longer influences the decision but stays visible in the explanation.
func Decide(invoiceNumber string, matchedPOs []string, checks []model.Check, waived map[string]bool, tolVersion int) Result {
	res := Result{InvoiceNumber: invoiceNumber, MatchedPOs: matchedPOs, RuleVersion: model.RuleVersion, ToleranceVersion: tolVersion}
	byClass := map[string][]model.Check{}
	for _, c := range checks {
		w := c.Status != model.Pass && c.ExceptionCode != "" && waived[c.ExceptionCode]
		res.Checks = append(res.Checks, CheckView{c.Code, c.Status, c.Expected, c.Actual, c.Reason, w})
		if c.Status == model.Pass || c.Status == model.NotRun || w || c.Class == "" {
			continue
		}
		byClass[c.Class] = append(byClass[c.Class], c)
	}
	res.Decision = Approve
	for _, p := range precedence {
		if cs := byClass[p.class]; len(cs) > 0 {
			res.Decision = p.decision
			res.DecidingClass = p.class
			var reasons []string
			for _, c := range cs {
				res.DecidingChecks = append(res.DecidingChecks, c.Code)
				reasons = append(reasons, c.Reason)
			}
			res.Summary = summarize(reasons)
			res.RecommendedAction = recommend(p.class, cs)
			break
		}
	}
	res.InvoiceStatus = statusFor(res.Decision, res.DecidingClass)
	if res.Decision == Approve {
		res.Summary = "All mandatory validation and matching checks passed."
		if len(matchedPOs) > 0 {
			res.Summary = fmt.Sprintf("All checks passed against %s and its goods receipts.", strings.Join(matchedPOs, ", "))
		}
		res.RecommendedAction = "Eligible for AP approval."
	}
	res.RequiresHumanReview = res.Decision != Approve
	return res
}

func statusFor(decision, class string) string {
	switch decision {
	case Approve:
		return "READY_FOR_VERIFICATION" // system checks passed; awaiting AP approval
	case Review:
		return "REVIEW_REQUIRED"
	case Hold:
		return "ON_HOLD"
	case Reject:
		return "REJECTED"
	case PendingDocuments:
		if class == model.ClassPendingPO {
			return "PENDING_PO_MATCH"
		}
		return "PENDING_GRN"
	}
	return "PENDING_DOCUMENTS"
}

func summarize(reasons []string) string {
	if len(reasons) == 1 {
		return reasons[0]
	}
	s := reasons[0]
	return fmt.Sprintf("%s (+%d more issue(s))", s, len(reasons)-1)
}

func recommend(class string, cs []model.Check) string {
	codes := map[string]bool{}
	for _, c := range cs {
		codes[c.ExceptionCode] = true
	}
	switch {
	case codes["QUANTITY_EXCEEDS_GRN"]:
		return "Ask AP/stores to verify the excess quantity against the goods receipt, or request a credit note from the supplier."
	case codes["QUANTITY_EXCEEDS_PO"]:
		return "Confirm with procurement whether a PO amendment exists; otherwise request a corrected invoice."
	case codes["PRICE_MISMATCH"]:
		return "Compare the charged price with the agreed PO price; request a credit note or a PO amendment."
	case codes["TAX_RATE_MISMATCH"], codes["TAX_CALCULATION_ERROR"]:
		return "Have tax/AP confirm the applicable GST and request a corrected invoice if needed."
	case codes["VENDOR_NOT_FOUND"], codes["VENDOR_NOT_APPROVED"], codes["VENDOR_IDENTITY_MISMATCH"]:
		return "Verify the supplier's identity and onboarding status with procurement before processing."
	}
	switch class {
	case model.ClassReject:
		return "Reject the invoice and notify the supplier."
	case model.ClassDuplicate:
		return "Compare with the earlier invoice; reject if it is a duplicate, or waive with a reason if it is a legitimate revision."
	case model.ClassMissing:
		return "Correct the extracted fields from the original document or request a complete invoice."
	case model.ClassPendingPO:
		return "Procurement should locate or raise the purchase order; the invoice will be re-evaluated automatically."
	case model.ClassPendingGRN:
		return "Wait for the goods receipt (or chase stores); the invoice will be re-evaluated automatically when it arrives."
	case model.ClassAmbiguous:
		return "Choose the correct purchase order from the candidates."
	case model.ClassBlocking:
		return "Resolve the listed mismatches with the supplier or procurement."
	case model.ClassLowConf:
		return "Verify the extracted values against the original document and correct them if needed."
	default:
		return "Review the warnings and approve, hold or reject."
	}
}
