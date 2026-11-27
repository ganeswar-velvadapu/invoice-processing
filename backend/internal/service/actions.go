package service

import (
	"context"
	"database/sql"
	"fmt"
	"invoiceproc/internal/utils"
	"strings"

	"invoiceproc/internal/db"
)

func requireReason(reason string) error {
	if len(strings.TrimSpace(reason)) < 5 {
		return utils.UserErr(utils.ErrInvalid, "a reason of at least 5 characters is required")
	}
	return nil
}

func (s *Service) invoiceState(ctx context.Context, q db.Querier, id string) (status, decision, docID string, err error) {
	var d sql.NullString
	err = q.QueryRowContext(ctx, `SELECT invoice_status, decision, document_id FROM invoices WHERE invoice_id=$1 FOR UPDATE`, id).Scan(&status, &d, &docID)
	if err == sql.ErrNoRows {
		err = utils.ErrNotFound
	}
	return status, d.String, docID, err
}

// requeueSiblings flags other open invoices sharing a PO for a re-check, since approving/rejecting
// changes how much quantity remains available to them. Callers wake the dispatcher after commit.
func (s *Service) requeueSiblings(ctx context.Context, q db.Querier, invoiceID string) {
	q.ExecContext(ctx, `UPDATE invoices SET verify_requested_at=now() WHERE invoice_id IN (
		SELECT m2.invoice_id FROM invoice_po_matches m1
		JOIN invoice_po_matches m2 ON m2.purchase_order_id=m1.purchase_order_id AND m2.invoice_id<>m1.invoice_id
		JOIN invoices i ON i.invoice_id=m2.invoice_id
		WHERE m1.invoice_id=$1 AND i.invoice_status NOT IN ('APPROVED','REJECTED','PAYMENT_PENDING','PAID'))`, invoiceID)
}

// Approve accepts an invoice whose system decision is APPROVE. Anything else needs an override.
func (s *Service) Approve(ctx context.Context, database *sql.DB, invoiceID, userID, note string) error {
	defer wakeUp() // after commit: re-check other invoices on the same PO
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, dec, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		if finalStatuses[status] {
			return utils.UserErr(utils.ErrConflict, "invoice is already "+status)
		}
		againstSystem := dec != "APPROVE"
		switch {
		case !againstSystem && (status == "READY_FOR_VERIFICATION" || status == "AWAITING_APPROVAL"):
		case againstSystem && status == "AWAITING_APPROVAL":
			// the clerk checked every exception and recommends approval; the manager accepts responsibility
			if err := requireReason(note); err != nil {
				return utils.UserErr(utils.ErrInvalid, fmt.Sprintf("the system decision is %s: approving needs a reason (min 5 characters)", dec))
			}
		default:
			return utils.UserErr(utils.ErrConflict, fmt.Sprintf("system decision is %s (status %s): send it for approval first, or use an override with a reason", dec, status))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status='APPROVED', requires_human_review=false, updated_at=now() WHERE invoice_id=$1`, invoiceID); err != nil {
			return err
		}
		if againstSystem {
			var checked []string
			rows, _ := tx.QueryContext(ctx, `SELECT exception_code || ': ' || COALESCE(resolution_note,'') FROM invoice_exceptions
				WHERE invoice_id=$1 AND exception_status IN ('CHECKED','OPEN','ASSIGNED')`, invoiceID)
			if rows != nil {
				for rows.Next() {
					var c string
					rows.Scan(&c)
					checked = append(checked, c)
				}
				rows.Close()
			}
			s.event(ctx, tx, invoiceID, docID, "APPROVED_AGAINST_SYSTEM", fmt.Sprintf("Approved although the system decision was %s: %s", dec, note), userID,
				map[string]any{"system_decision": dec, "reason": note, "exceptions_at_approval": checked})
			s.requeueSiblings(ctx, tx, invoiceID)
			return nil
		}
		s.event(ctx, tx, invoiceID, docID, "APPROVED", "Invoice approved by AP"+suffix(note), userID, map[string]any{"previous_status": status, "note": note})
		s.requeueSiblings(ctx, tx, invoiceID)
		return nil
	})
}

func (s *Service) Reject(ctx context.Context, database *sql.DB, invoiceID, userID, reason string) error {
	defer wakeUp()
	if err := requireReason(reason); err != nil {
		return err
	}
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, dec, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		if status == "REJECTED" || status == "PAID" || status == "PAYMENT_PENDING" {
			return utils.UserErr(utils.ErrConflict, "invoice is already "+status)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status='REJECTED', requires_human_review=false, updated_at=now() WHERE invoice_id=$1`, invoiceID); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, docID, "REJECTED", "Invoice rejected: "+reason, userID, map[string]any{"previous_status": status, "system_decision": dec, "reason": reason})
		s.requeueSiblings(ctx, tx, invoiceID)
		return nil
	})
}

var overrideTargets = map[string]bool{"APPROVED": true, "REJECTED": true, "ON_HOLD": true, "REVIEW_REQUIRED": true}

// Override sets a status against the system decision; reason is mandatory and fully audited.
func (s *Service) Override(ctx context.Context, database *sql.DB, invoiceID, userID, target, reason string) error {
	defer wakeUp()
	target = strings.ToUpper(target)
	if !overrideTargets[target] {
		return utils.UserErr(utils.ErrInvalid, "target_status must be one of APPROVED, REJECTED, ON_HOLD, REVIEW_REQUIRED")
	}
	if err := requireReason(reason); err != nil {
		return err
	}
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, dec, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		if status == "PAID" || status == "PAYMENT_PENDING" {
			return utils.UserErr(utils.ErrConflict, "invoice is already in payment ("+status+")")
		}
		var open []string
		rows, _ := tx.QueryContext(ctx, `SELECT exception_code FROM invoice_exceptions WHERE invoice_id=$1 AND exception_status IN ('OPEN','ASSIGNED')`, invoiceID)
		if rows != nil {
			for rows.Next() {
				var c string
				rows.Scan(&c)
				open = append(open, c)
			}
			rows.Close()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status=$2, updated_at=now() WHERE invoice_id=$1`, invoiceID, target); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, docID, "OVERRIDE", fmt.Sprintf("Manual override %s → %s (system decision %s): %s", status, target, dec, reason), userID,
			map[string]any{"previous_status": status, "target_status": target, "system_decision": dec, "open_exceptions": open, "reason": reason})
		s.requeueSiblings(ctx, tx, invoiceID)
		return nil
	})
}

// UpdateException resolves, waives, reopens or assigns an exception, then re-runs the decision.
func (s *Service) UpdateException(ctx context.Context, database *sql.DB, excID, userID, action, note, assignee string) (string, error) {
	var invoiceID, code, status string
	if err := database.QueryRowContext(ctx, `SELECT invoice_id, exception_code, exception_status FROM invoice_exceptions WHERE invoice_exception_id=$1`, excID).
		Scan(&invoiceID, &code, &status); err != nil {
		if err == sql.ErrNoRows {
			return "", utils.ErrNotFound
		}
		return "", err
	}
	var q string
	args := []any{excID}
	switch strings.ToUpper(action) {
	case "RESOLVE":
		if err := requireReason(note); err != nil {
			return "", err
		}
		q = `UPDATE invoice_exceptions SET exception_status='RESOLVED', resolution_note=$2, resolved_by_user_id=$3, resolved_at=now() WHERE invoice_exception_id=$1`
		args = append(args, note, userID)
	case "WAIVE":
		if err := requireReason(note); err != nil {
			return "", err
		}
		q = `UPDATE invoice_exceptions SET exception_status='WAIVED', resolution_note=$2, resolved_by_user_id=$3, resolved_at=now() WHERE invoice_exception_id=$1`
		args = append(args, note, userID)
	case "CHECK":
		// a person has looked at the issue against the PO/GRN/original and explains it; it still counts
		// for the system decision, and the manager sees the explanation when approving
		if err := requireReason(note); err != nil {
			return "", err
		}
		q = `UPDATE invoice_exceptions SET exception_status='CHECKED', resolution_note=$2, resolved_by_user_id=$3, resolved_at=now() WHERE invoice_exception_id=$1`
		args = append(args, note, userID)
	case "REOPEN":
		q = `UPDATE invoice_exceptions SET exception_status='OPEN', resolution_note=NULL, resolved_by_user_id=NULL, resolved_at=NULL WHERE invoice_exception_id=$1`
	case "ASSIGN":
		q = `UPDATE invoice_exceptions SET exception_status='ASSIGNED', assigned_to_user_id=$2 WHERE invoice_exception_id=$1`
		args = append(args, nullable(assignee))
	default:
		return "", utils.UserErr(utils.ErrInvalid, "action must be CHECK, RESOLVE, WAIVE, REOPEN or ASSIGN")
	}
	if _, err := database.ExecContext(ctx, q, args...); err != nil {
		return "", err
	}
	s.event(ctx, database, invoiceID, "", "EXCEPTION_"+strings.ToUpper(action), fmt.Sprintf("%s %s: %s", strings.ToLower(action), code, note), userID,
		map[string]any{"exception_id": excID, "previous_status": status, "assignee": assignee})
	// A resolved exception whose underlying check still fails will be reopened by verification;
	// a waived one is excluded from the decision.
	_, err := s.Verify(ctx, database, invoiceID, userID)
	return invoiceID, err
}

// SetPOMatch lets AP confirm or reject a PO for an invoice (MANUAL match), or clear manual choices.
func (s *Service) SetPOMatch(ctx context.Context, database *sql.DB, invoiceID, poID, userID, action, note string) error {
	action = strings.ToUpper(action)
	err := db.InTx(ctx, database, func(tx *sql.Tx) error {
		var poNum string
		if action != "CLEAR" {
			if err := tx.QueryRowContext(ctx, `SELECT po_number FROM purchase_orders WHERE purchase_order_id=$1`, poID).Scan(&poNum); err != nil {
				return utils.UserErr(utils.ErrNotFound, "purchase order not found")
			}
		}
		switch action {
		case "CONFIRM", "REJECT":
			st := map[string]string{"CONFIRM": "CONFIRMED", "REJECT": "REJECTED"}[action]
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_po_matches (invoice_id, purchase_order_id, match_method, match_status, match_confidence,
				evidence, matched_by_user_id) VALUES ($1,$2,'MANUAL',$3,1,$4::jsonb,$5)
				ON CONFLICT (invoice_id, purchase_order_id) DO UPDATE SET match_method='MANUAL', match_status=EXCLUDED.match_status,
				evidence=EXCLUDED.evidence, matched_by_user_id=EXCLUDED.matched_by_user_id`,
				invoiceID, poID, st, jsonb([]string{"set manually: " + note}), userID); err != nil {
				return err
			}
			s.event(ctx, tx, invoiceID, "", "PO_MATCH_"+st, fmt.Sprintf("PO %s %s manually%s", poNum, strings.ToLower(st), suffix(note)), userID, map[string]any{"purchase_order_id": poID})
		case "CLEAR":
			if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_po_matches WHERE invoice_id=$1 AND match_method='MANUAL'`, invoiceID); err != nil {
				return err
			}
			s.event(ctx, tx, invoiceID, "", "PO_MATCH_CLEARED", "Manual PO selections cleared", userID, nil)
		default:
			return utils.UserErr(utils.ErrInvalid, "action must be CONFIRM, REJECT or CLEAR")
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, err = s.Verify(ctx, database, invoiceID, userID)
	return err
}

func suffix(note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return ": " + note
}

// ReturnToClerk sends an invoice back to AP clerks with the manager's note. It stays RETURNED (automatic
// re-checks do not change that) until a clerk resubmits it.
func (s *Service) ReturnToClerk(ctx context.Context, database *sql.DB, invoiceID, userID, note, assignee string) error {
	if err := requireReason(note); err != nil {
		return err
	}
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, dec, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		if finalStatuses[status] {
			return utils.UserErr(utils.ErrConflict, "invoice is already "+status)
		}
		if status == "RETURNED" {
			return utils.UserErr(utils.ErrConflict, "invoice is already returned to the clerk")
		}
		if assignee == "" { // default: the clerk who uploaded it
			tx.QueryRowContext(ctx, `SELECT COALESCE(d.uploaded_by_user_id::text,'') FROM documents d JOIN users u ON u.user_id=d.uploaded_by_user_id
				WHERE d.document_id=$1 AND u.role='AP_CLERK'`, docID).Scan(&assignee)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status='RETURNED', return_note=$2, returned_by_user_id=$3, returned_at=now(),
			assigned_to_user_id=$4, updated_at=now() WHERE invoice_id=$1`, invoiceID, note, userID, nullable(assignee)); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, docID, "RETURNED_TO_CLERK", "Returned to clerk: "+note, userID,
			map[string]any{"previous_status": status, "system_decision": dec, "note": note, "assigned_to_user_id": assignee})
		return nil
	})
}

// Resubmit: a clerk sends a returned invoice back for approval. It is re-verified, so the manager sees a
// fresh system decision based on whatever the clerk corrected.
func (s *Service) Resubmit(ctx context.Context, database *sql.DB, invoiceID, userID, note string) error {
	if err := requireReason(note); err != nil {
		return err
	}
	err := db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, _, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		if status != "RETURNED" {
			return utils.UserErr(utils.ErrConflict, "only invoices returned by a manager can be resubmitted (status is "+status+")")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status='REVIEW_REQUIRED', updated_at=now() WHERE invoice_id=$1`, invoiceID); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, docID, "RESUBMITTED", "Resubmitted for approval: "+note, userID, map[string]any{"note": note})
		return nil
	})
	if err != nil {
		return err
	}
	_, err = s.Verify(ctx, database, invoiceID, userID)
	return err
}

// SubmitForApproval: a clerk has reviewed the invoice against its PO/GRN and sends it to a manager with a
// recommendation. Recommending approval requires every exception to be checked, resolved or waived first.
func (s *Service) SubmitForApproval(ctx context.Context, database *sql.DB, invoiceID, userID, recommendation, note string) error {
	recommendation = strings.ToUpper(recommendation)
	if recommendation != "APPROVE" && recommendation != "REJECT" {
		return utils.UserErr(utils.ErrInvalid, "recommendation must be APPROVE or REJECT")
	}
	if err := requireReason(note); err != nil {
		return err
	}
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		status, dec, docID, err := s.invoiceState(ctx, tx, invoiceID)
		if err != nil {
			return err
		}
		switch status {
		case "ON_HOLD", "REVIEW_REQUIRED", "READY_FOR_VERIFICATION", "RETURNED":
		case "PENDING_PO_MATCH", "PENDING_GRN", "PENDING_DOCUMENTS":
			if recommendation == "APPROVE" {
				return utils.UserErr(utils.ErrConflict, "the purchase order or goods receipt is not in yet: an invoice cannot be recommended for approval before it can be matched")
			}
		default:
			return utils.UserErr(utils.ErrConflict, "invoice is "+status+" and cannot be sent for approval")
		}
		if recommendation == "APPROVE" {
			var open []string
			rows, _ := tx.QueryContext(ctx, `SELECT exception_code FROM invoice_exceptions WHERE invoice_id=$1 AND exception_status IN ('OPEN','ASSIGNED')`, invoiceID)
			if rows != nil {
				for rows.Next() {
					var c string
					rows.Scan(&c)
					open = append(open, c)
				}
				rows.Close()
			}
			if len(open) > 0 {
				return utils.UserErr(utils.ErrConflict, "check every exception first (mark it checked with a note, or resolve it): still open: "+strings.Join(open, ", "))
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET invoice_status='AWAITING_APPROVAL', submitted_by_user_id=$2, submitted_at=now(),
			clerk_recommendation=$3, clerk_note=$4, updated_at=now() WHERE invoice_id=$1`, invoiceID, userID, recommendation, note); err != nil {
			return err
		}
		s.event(ctx, tx, invoiceID, docID, "SENT_FOR_APPROVAL", fmt.Sprintf("Sent for approval, recommending %s: %s", strings.ToLower(recommendation), note), userID,
			map[string]any{"recommendation": recommendation, "note": note, "previous_status": status, "system_decision": dec})
		return nil
	})
}
