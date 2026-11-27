package service

import (
	"context"
	"database/sql"
	"fmt"
	"invoiceproc/internal/utils"

	"invoiceproc/internal/db"
	"invoiceproc/internal/gstin"
)

// ProcurementImport is the JSON import format (also the target of CSV conversion).
type ProcurementImport struct {
	Vendors           []VendorInput `json:"vendors"`
	PurchaseOrders    []POInput     `json:"purchase_orders"`
	GoodsReceiptNotes []GRNInput    `json:"goods_receipt_notes"`
}

type ImportResult struct {
	Vendors           int      `json:"vendors"`
	PurchaseOrders    int      `json:"purchase_orders"`
	GoodsReceiptNotes int      `json:"goods_receipt_notes"`
	Errors            []string `json:"errors"`
	Warnings          []string `json:"warnings"`
}

// Import loads master/procurement data atomically (all or nothing) and re-evaluates open invoices.
func (s *Service) Import(ctx context.Context, database *sql.DB, in ProcurementImport, source, userID string) (*ImportResult, error) {
	if source == "" {
		source = "IMPORT"
	}
	res := &ImportResult{Errors: []string{}, Warnings: []string{}}
	err := db.InTx(ctx, database, func(tx *sql.Tx) error {
		for i, v := range in.Vendors {
			if _, err := s.UpsertVendor(ctx, tx, v); err != nil {
				return fmt.Errorf("vendor #%d (%s): %w", i+1, v.LegalName, err)
			}
			if _, ignored := gstin.Check(v.GSTIN); ignored {
				res.Warnings = append(res.Warnings, fmt.Sprintf("vendor %s: GSTIN %s has an invalid checksum digit (accepted because GSTIN_CHECKSUM=%s)",
					v.LegalName, v.GSTIN, gstin.ChecksumMode()))
			}
			res.Vendors++
		}
		for _, p := range in.PurchaseOrders {
			p.Source = source
			if _, err := s.upsertPO(ctx, tx, p, userID); err != nil {
				return fmt.Errorf("PO %s: %w", p.PONumber, err)
			}
			res.PurchaseOrders++
		}
		for _, g := range in.GoodsReceiptNotes {
			g.Source = source
			if _, err := s.upsertGRN(ctx, tx, g, userID); err != nil {
				return fmt.Errorf("GRN %s: %w", g.GRNNumber, err)
			}
			res.GoodsReceiptNotes++
		}
		s.event(ctx, tx, "", "", "PROCUREMENT_IMPORTED", fmt.Sprintf("Imported %d vendors, %d POs, %d GRNs", res.Vendors, res.PurchaseOrders, res.GoodsReceiptNotes), userID, res)
		return nil
	})
	if err != nil {
		return nil, utils.UserErr(utils.ErrInvalid, "import failed, nothing was saved: "+err.Error())
	}
	return res, s.relink(ctx, database)
}
