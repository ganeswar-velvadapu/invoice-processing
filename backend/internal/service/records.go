package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"invoiceproc/internal/db"
	"invoiceproc/internal/gstin"
	"invoiceproc/internal/parse"
	"invoiceproc/internal/util"
)

// ---------------------------------------------------------------- procurement inputs (import + documents)

type VendorInput struct {
	LegalName    string `json:"legal_name"`
	GSTIN        string `json:"gstin"`
	Address      string `json:"address"`
	IsApproved   bool   `json:"is_approved"`
	VendorStatus string `json:"vendor_status"`
}

type POItemInput struct {
	LineNumber      int     `json:"line_number"`
	ItemCode        string  `json:"item_code"`
	Description     string  `json:"description"`
	HSNSAC          string  `json:"hsn_sac"`
	QuantityOrdered float64 `json:"quantity_ordered"`
	UnitOfMeasure   string  `json:"unit_of_measure"`
	UnitPrice       float64 `json:"unit_price"`
	TaxRate         float64 `json:"tax_rate"`
}

type POInput struct {
	PONumber    string        `json:"po_number"`
	VendorGSTIN string        `json:"vendor_gstin"`
	PODate      string        `json:"po_date"`
	BuyerEntity string        `json:"buyer_entity"`
	BuyerGSTIN  string        `json:"buyer_gstin"`
	Currency    string        `json:"currency"`
	POStatus    string        `json:"po_status"`
	TotalAmount *float64      `json:"total_amount"`
	Items       []POItemInput `json:"items"`
	DocumentID  string        `json:"-"`
	Source      string        `json:"-"`
}

type GRNItemInput struct {
	LineNumber       int     `json:"line_number"`
	ItemCode         string  `json:"item_code"`
	Description      string  `json:"description"`
	QuantityReceived float64 `json:"quantity_received"`
	QuantityAccepted float64 `json:"quantity_accepted"`
	QuantityRejected float64 `json:"quantity_rejected"`
	UnitOfMeasure    string  `json:"unit_of_measure"`
}

type GRNInput struct {
	GRNNumber     string         `json:"grn_number"`
	PONumber      string         `json:"po_number"`
	VendorGSTIN   string         `json:"vendor_gstin"`
	ReceiptDate   string         `json:"receipt_date"`
	ReceiptStatus string         `json:"receipt_status"`
	Items         []GRNItemInput `json:"items"`
	DocumentID    string         `json:"-"`
	Source        string         `json:"-"`
}

func fieldVal(d *parse.Data, k string) string {
	if f := d.Fields[k]; f != nil {
		return f.Value
	}
	return ""
}

func fieldNum(d *parse.Data, k string) *float64 {
	if v := fieldVal(d, k); v != "" {
		if f, ok := parse.ParseNumber(v); ok {
			return &f
		}
	}
	return nil
}

func fieldDate(d *parse.Data, k string) *time.Time {
	v := fieldVal(d, k)
	if v == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return &t
	}
	if t, ok := parse.ParseDate(v); ok {
		return &t
	}
	return nil
}

func poInputFromData(d *parse.Data, docID string) (POInput, error) {
	in := POInput{PONumber: fieldVal(d, "po_number"), VendorGSTIN: fieldVal(d, "vendor_gstin"), BuyerEntity: fieldVal(d, "buyer_name"),
		BuyerGSTIN: fieldVal(d, "buyer_gstin"), Currency: fieldVal(d, "currency"), TotalAmount: fieldNum(d, "total_amount"),
		DocumentID: docID, Source: "DOCUMENT", POStatus: "APPROVED"}
	if t := fieldDate(d, "po_date"); t != nil {
		in.PODate = t.Format("2006-01-02")
	}
	if in.PONumber == "" {
		return in, errors.New("PO number could not be extracted; correct the extraction to create the PO record")
	}
	for _, it := range d.Items {
		in.Items = append(in.Items, POItemInput{LineNumber: it.LineNumber, ItemCode: it.ItemCode, Description: it.Description, HSNSAC: it.HSNSAC,
			QuantityOrdered: util.F(it.Quantity), UnitOfMeasure: it.Unit, UnitPrice: util.F(it.UnitPrice), TaxRate: util.F(it.TaxRate)})
	}
	return in, nil
}

func grnInputFromData(d *parse.Data, docID string) (GRNInput, error) {
	in := GRNInput{GRNNumber: fieldVal(d, "grn_number"), VendorGSTIN: fieldVal(d, "vendor_gstin"), DocumentID: docID, Source: "DOCUMENT"}
	if refs := strings.Split(fieldVal(d, "po_reference"), ","); refs[0] != "" {
		in.PONumber = strings.TrimSpace(refs[0])
	}
	if t := fieldDate(d, "receipt_date"); t != nil {
		in.ReceiptDate = t.Format("2006-01-02")
	}
	if in.GRNNumber == "" {
		return in, errors.New("GRN number could not be extracted; correct the extraction to create the GRN record")
	}
	for _, it := range d.Items {
		in.Items = append(in.Items, GRNItemInput{LineNumber: it.LineNumber, ItemCode: it.ItemCode, Description: it.Description,
			QuantityReceived: util.F(it.QuantityReceived), QuantityAccepted: util.F(it.QuantityAccepted), QuantityRejected: util.F(it.QuantityRejected),
			UnitOfMeasure: it.Unit})
	}
	return in, nil
}

func dateOrNil(s string) any {
	if s == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if t, ok := parse.ParseDate(s); ok {
		return t
	}
	return nil
}

// ---------------------------------------------------------------- vendors

func (s *Service) UpsertVendor(ctx context.Context, q db.Querier, v VendorInput) (string, error) {
	g := gstin.Normalize(v.GSTIN)
	if strings.TrimSpace(v.LegalName) == "" {
		return "", userErr(ErrInvalid, "legal_name is required")
	}
	if g != "" {
		if r, _ := gstin.Check(g); !r.Valid {
			return "", userErr(ErrInvalid, "invalid GSTIN "+g+": "+r.Reason)
		}
	}
	if v.VendorStatus == "" {
		v.VendorStatus = "ACTIVE"
	}
	var id string
	if g == "" {
		err := q.QueryRowContext(ctx, `INSERT INTO vendors (legal_name, address, is_approved, vendor_status) VALUES ($1,$2,$3,$4) RETURNING vendor_id`,
			v.LegalName, v.Address, v.IsApproved, v.VendorStatus).Scan(&id)
		return id, err
	}
	err := q.QueryRowContext(ctx, `INSERT INTO vendors (legal_name, gstin, address, is_approved, vendor_status) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (gstin) DO UPDATE SET legal_name=EXCLUDED.legal_name, address=EXCLUDED.address, is_approved=EXCLUDED.is_approved,
		vendor_status=EXCLUDED.vendor_status, updated_at=now() RETURNING vendor_id`, v.LegalName, g, v.Address, v.IsApproved, v.VendorStatus).Scan(&id)
	return id, err
}

func vendorIDByGSTIN(ctx context.Context, q db.Querier, g string) string {
	if g == "" {
		return ""
	}
	var id string
	q.QueryRowContext(ctx, `SELECT vendor_id FROM vendors WHERE gstin=$1`, gstin.Normalize(g)).Scan(&id)
	return id
}

// ---------------------------------------------------------------- invoices

func (s *Service) materializeInvoice(ctx context.Context, tx *sql.Tx, docID string, d *parse.Data, conf float64, userID string) (string, error) {
	num := fieldVal(d, "invoice_number")
	norm := util.NormalizeDocNumber(num)
	date := fieldDate(d, "invoice_date")
	fy := ""
	if date != nil {
		fy = util.FinancialYear(*date)
	}
	seller := gstin.Normalize(fieldVal(d, "seller_gstin"))
	vendorID := vendorIDByGSTIN(ctx, tx, seller)
	var refs []string
	for _, r := range strings.Split(fieldVal(d, "po_reference"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			refs = append(refs, r)
		}
	}
	if refs == nil {
		refs = []string{}
	}
	cur := fieldVal(d, "currency")
	if cur == "" {
		cur = "INR"
	}

	var existingID string
	tx.QueryRowContext(ctx, `SELECT invoice_id FROM invoices WHERE document_id=$1`, docID).Scan(&existingID)

	// Business duplicate: an earlier live invoice with the same supplier + number + FY.
	dupOf := ""
	if seller != "" && norm != "" && fy != "" {
		tx.QueryRowContext(ctx, `SELECT invoice_id FROM invoices WHERE seller_gstin=$1 AND invoice_number_normalized=$2 AND financial_year=$3
			AND invoice_status <> 'REJECTED' AND duplicate_of_invoice_id IS NULL AND is_revision = FALSE
			AND invoice_id IS DISTINCT FROM NULLIF($4,'')::uuid ORDER BY created_at LIMIT 1`, seller, norm, fy, existingID).Scan(&dupOf)
	}
	if dupOf == "" {
		// identical file processed before
		tx.QueryRowContext(ctx, `SELECT i.invoice_id FROM documents d JOIN invoices i ON i.document_id = d.duplicate_of_document_id
			WHERE d.document_id=$1 AND i.invoice_status <> 'REJECTED'`, docID).Scan(&dupOf)
	}

	args := []any{docID, nullable(num), nullable(norm), dateOrNilT(date), dateOrNilT(fieldDate(d, "due_date")), nullable(fy), nullable(vendorID),
		nullable(fieldVal(d, "seller_name")), nullable(seller), nullable(fieldVal(d, "seller_address")), nullable(fieldVal(d, "buyer_name")),
		nullable(gstin.Normalize(fieldVal(d, "buyer_gstin"))), refs, cur, nullable(fieldVal(d, "payment_terms")),
		fieldNum(d, "subtotal"), fieldNum(d, "taxable_amount"), fieldNum(d, "cgst"), fieldNum(d, "sgst"), fieldNum(d, "igst"),
		fieldNum(d, "other_charges"), fieldNum(d, "round_off"), fieldNum(d, "total_tax"), fieldNum(d, "grand_total"), conf, nullable(dupOf)}
	const upsert = `INSERT INTO invoices (document_id, invoice_number, invoice_number_normalized, invoice_date, due_date, financial_year, vendor_id,
		seller_name, seller_gstin, seller_address, buyer_name, buyer_gstin, po_references, currency, payment_terms,
		subtotal, taxable_amount, cgst_amount, sgst_amount, igst_amount, other_charges, round_off, total_tax, grand_total,
		extraction_confidence, duplicate_of_invoice_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
		ON CONFLICT (document_id) DO UPDATE SET invoice_number=EXCLUDED.invoice_number, invoice_number_normalized=EXCLUDED.invoice_number_normalized,
		invoice_date=EXCLUDED.invoice_date, due_date=EXCLUDED.due_date, financial_year=EXCLUDED.financial_year, vendor_id=EXCLUDED.vendor_id,
		seller_name=EXCLUDED.seller_name, seller_gstin=EXCLUDED.seller_gstin, seller_address=EXCLUDED.seller_address, buyer_name=EXCLUDED.buyer_name,
		buyer_gstin=EXCLUDED.buyer_gstin, po_references=EXCLUDED.po_references, currency=EXCLUDED.currency, payment_terms=EXCLUDED.payment_terms,
		subtotal=EXCLUDED.subtotal, taxable_amount=EXCLUDED.taxable_amount, cgst_amount=EXCLUDED.cgst_amount, sgst_amount=EXCLUDED.sgst_amount,
		igst_amount=EXCLUDED.igst_amount, other_charges=EXCLUDED.other_charges, round_off=EXCLUDED.round_off, total_tax=EXCLUDED.total_tax,
		grand_total=EXCLUDED.grand_total, extraction_confidence=EXCLUDED.extraction_confidence,
		duplicate_of_invoice_id=EXCLUDED.duplicate_of_invoice_id, updated_at=now()
		RETURNING invoice_id`
	var id string
	if _, err := tx.ExecContext(ctx, `SAVEPOINT inv_upsert`); err != nil {
		return "", err
	}
	err := tx.QueryRowContext(ctx, upsert, args...).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "business_key") {
		// lost a race with a concurrent identical invoice: store this one as its duplicate
		tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT inv_upsert`)
		tx.QueryRowContext(ctx, `SELECT invoice_id FROM invoices WHERE seller_gstin=$1 AND invoice_number_normalized=$2 AND financial_year=$3
			AND duplicate_of_invoice_id IS NULL AND is_revision=FALSE AND invoice_status<>'REJECTED' LIMIT 1`, seller, norm, fy).Scan(&dupOf)
		args[len(args)-1] = nullable(dupOf)
		err = tx.QueryRowContext(ctx, upsert, args...).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("upsert invoice: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_items WHERE invoice_id=$1`, id); err != nil {
		return "", err
	}
	for _, it := range d.Items {
		disc := 0.0
		if it.Discount != nil {
			disc = *it.Discount
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_items (invoice_id, line_number, item_code, description, hsn_sac, quantity,
			unit_of_measure, unit_price, discount, taxable_value, tax_rate, tax_amount, line_total, confidence)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			id, it.LineNumber, it.ItemCode, it.Description, it.HSNSAC, it.Quantity, it.Unit, it.UnitPrice, disc, it.TaxableValue,
			it.TaxRate, it.TaxAmount, it.LineTotal, it.Confidence); err != nil {
			return "", err
		}
	}
	desc := fmt.Sprintf("Invoice %s recorded from extraction", firstNonEmpty(num, "(no number)"))
	if existingID != "" {
		desc = fmt.Sprintf("Invoice %s updated from new extraction", firstNonEmpty(num, "(no number)"))
	}
	s.event(ctx, tx, id, docID, "INVOICE_RECORDED", desc, userID, map[string]any{"duplicate_of_invoice_id": dupOf})
	return id, nil
}

func dateOrNilT(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------- purchase orders

// upsertPO creates or updates a PO. Imported (master) data is never overwritten by document extraction:
// an uploaded PO document whose number already exists is only linked to that record.
func (s *Service) upsertPO(ctx context.Context, q db.Querier, in POInput, userID string) (string, error) {
	norm := util.NormalizeRef(in.PONumber)
	if norm == "" {
		return "", userErr(ErrInvalid, "po_number is required")
	}
	if in.Source == "" {
		in.Source = "IMPORT"
	}
	if in.POStatus == "" {
		in.POStatus = "APPROVED"
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}
	g := gstin.Normalize(in.VendorGSTIN)
	vendorID := vendorIDByGSTIN(ctx, q, g)
	var id, existingSource string
	err := q.QueryRowContext(ctx, `SELECT purchase_order_id, source FROM purchase_orders WHERE po_number_normalized=$1`, norm).Scan(&id, &existingSource)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if id != "" && in.Source == "DOCUMENT" && existingSource != "DOCUMENT" {
		q.ExecContext(ctx, `UPDATE purchase_orders SET document_id=COALESCE(document_id,$2) WHERE purchase_order_id=$1`, id, nullable(in.DocumentID))
		s.event(ctx, q, "", in.DocumentID, "PO_DOCUMENT_LINKED", fmt.Sprintf("Uploaded PO %s linked to the existing %s record (master data kept)", in.PONumber, existingSource), userID, nil)
		return id, nil
	}
	total := in.TotalAmount
	if total == nil {
		var t float64
		for _, it := range in.Items {
			t += it.QuantityOrdered * it.UnitPrice
		}
		t = util.Round2(t)
		total = &t
	}
	if id == "" {
		err = q.QueryRowContext(ctx, `INSERT INTO purchase_orders (document_id, po_number, po_number_normalized, vendor_id, vendor_gstin, buyer_entity,
			buyer_gstin, po_date, currency, po_status, total_amount, source) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING purchase_order_id`,
			nullable(in.DocumentID), in.PONumber, norm, nullable(vendorID), nullable(g), in.BuyerEntity, nullable(gstin.Normalize(in.BuyerGSTIN)),
			dateOrNil(in.PODate), in.Currency, in.POStatus, total, in.Source).Scan(&id)
	} else {
		_, err = q.ExecContext(ctx, `UPDATE purchase_orders SET document_id=COALESCE($2, document_id), po_number=$3, vendor_id=$4, vendor_gstin=$5,
			buyer_entity=$6, buyer_gstin=$7, po_date=$8, currency=$9, po_status=$10, total_amount=$11, source=$12, updated_at=now() WHERE purchase_order_id=$1`,
			id, nullable(in.DocumentID), in.PONumber, nullable(vendorID), nullable(g), in.BuyerEntity, nullable(gstin.Normalize(in.BuyerGSTIN)),
			dateOrNil(in.PODate), in.Currency, in.POStatus, total, in.Source)
	}
	if err != nil {
		return "", fmt.Errorf("upsert PO %s: %w", in.PONumber, err)
	}
	for i, it := range in.Items {
		ln := it.LineNumber
		if ln == 0 {
			ln = i + 1
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO purchase_order_items (purchase_order_id, line_number, item_code, description, hsn_sac,
			quantity_ordered, unit_of_measure, unit_price, tax_rate, line_total) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (purchase_order_id, line_number) DO UPDATE SET item_code=EXCLUDED.item_code, description=EXCLUDED.description,
			hsn_sac=EXCLUDED.hsn_sac, quantity_ordered=EXCLUDED.quantity_ordered, unit_of_measure=EXCLUDED.unit_of_measure,
			unit_price=EXCLUDED.unit_price, tax_rate=EXCLUDED.tax_rate, line_total=EXCLUDED.line_total`,
			id, ln, it.ItemCode, it.Description, it.HSNSAC, it.QuantityOrdered, it.UnitOfMeasure, it.UnitPrice, it.TaxRate,
			util.Round2(it.QuantityOrdered*it.UnitPrice)); err != nil {
			return "", err
		}
	}
	s.event(ctx, q, "", in.DocumentID, "PO_RECORDED", fmt.Sprintf("Purchase order %s recorded (%s, %d lines)", in.PONumber, in.Source, len(in.Items)), userID,
		map[string]any{"purchase_order_id": id})
	return id, nil
}

// ---------------------------------------------------------------- goods receipts

func (s *Service) upsertGRN(ctx context.Context, q db.Querier, in GRNInput, userID string) (string, error) {
	norm := util.NormalizeRef(in.GRNNumber)
	if norm == "" {
		return "", userErr(ErrInvalid, "grn_number is required")
	}
	if in.Source == "" {
		in.Source = "IMPORT"
	}
	if in.ReceiptStatus == "" {
		in.ReceiptStatus = "RECEIVED"
	}
	g := gstin.Normalize(in.VendorGSTIN)
	var poID, poVendor string
	if in.PONumber != "" {
		if id, _, _ := findPOByRef(ctx, q, in.PONumber, vendorIDByGSTIN(ctx, q, g), g); id != "" {
			poID = id
			q.QueryRowContext(ctx, `SELECT COALESCE(vendor_id::text,'') FROM purchase_orders WHERE purchase_order_id=$1`, id).Scan(&poVendor)
		}
	}
	vendorID := firstNonEmpty(vendorIDByGSTIN(ctx, q, g), poVendor)
	var id, existingSource string
	q.QueryRowContext(ctx, `SELECT goods_receipt_note_id, source FROM goods_receipt_notes WHERE grn_number_normalized=$1`, norm).Scan(&id, &existingSource)
	if id != "" && in.Source == "DOCUMENT" && existingSource != "DOCUMENT" {
		q.ExecContext(ctx, `UPDATE goods_receipt_notes SET document_id=COALESCE(document_id,$2) WHERE goods_receipt_note_id=$1`, id, nullable(in.DocumentID))
		s.event(ctx, q, "", in.DocumentID, "GRN_DOCUMENT_LINKED", fmt.Sprintf("Uploaded GRN %s linked to the existing %s record", in.GRNNumber, existingSource), userID, nil)
		return id, nil
	}
	var err error
	if id == "" {
		err = q.QueryRowContext(ctx, `INSERT INTO goods_receipt_notes (document_id, grn_number, grn_number_normalized, po_reference, purchase_order_id,
			vendor_id, vendor_gstin, receipt_date, receipt_status, source) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING goods_receipt_note_id`,
			nullable(in.DocumentID), in.GRNNumber, norm, nullable(in.PONumber), nullable(poID), nullable(vendorID), nullable(g),
			dateOrNil(in.ReceiptDate), in.ReceiptStatus, in.Source).Scan(&id)
	} else {
		_, err = q.ExecContext(ctx, `UPDATE goods_receipt_notes SET document_id=COALESCE($2,document_id), grn_number=$3, po_reference=$4,
			purchase_order_id=$5, vendor_id=$6, vendor_gstin=$7, receipt_date=$8, receipt_status=$9, source=$10 WHERE goods_receipt_note_id=$1`,
			id, nullable(in.DocumentID), in.GRNNumber, nullable(in.PONumber), nullable(poID), nullable(vendorID), nullable(g),
			dateOrNil(in.ReceiptDate), in.ReceiptStatus, in.Source)
	}
	if err != nil {
		return "", fmt.Errorf("upsert GRN %s: %w", in.GRNNumber, err)
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM goods_receipt_items WHERE goods_receipt_note_id=$1`, id); err != nil {
		return "", err
	}
	for i, it := range in.Items {
		ln := it.LineNumber
		if ln == 0 {
			ln = i + 1
		}
		acc := it.QuantityAccepted
		if acc == 0 && it.QuantityRejected == 0 {
			acc = it.QuantityReceived
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO goods_receipt_items (goods_receipt_note_id, line_number, item_code, description,
			quantity_received, quantity_accepted, quantity_rejected, unit_of_measure) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, ln, it.ItemCode, it.Description, it.QuantityReceived, acc, it.QuantityRejected, it.UnitOfMeasure); err != nil {
			return "", err
		}
	}
	if poID != "" {
		if err := s.linkGRN(ctx, q, id, poID); err != nil {
			return "", err
		}
	}
	desc := fmt.Sprintf("Goods receipt %s recorded (%s, %d lines)", in.GRNNumber, in.Source, len(in.Items))
	if poID == "" && in.PONumber != "" {
		desc += "; PO " + in.PONumber + " not on file yet, will link when it arrives"
	}
	s.event(ctx, q, "", in.DocumentID, "GRN_RECORDED", desc, userID, map[string]any{"goods_receipt_note_id": id})
	return id, nil
}

// linkGRN links a GRN and its lines to a PO (item code, then description, then line number).
func (s *Service) linkGRN(ctx context.Context, q db.Querier, grnID, poID string) error {
	if _, err := q.ExecContext(ctx, `UPDATE goods_receipt_notes SET purchase_order_id=$2 WHERE goods_receipt_note_id=$1`, grnID, poID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO grn_po_matches (goods_receipt_note_id, purchase_order_id, match_method)
		VALUES ($1,$2,'EXACT_REFERENCE') ON CONFLICT DO NOTHING`, grnID, poID); err != nil {
		return err
	}
	type poi struct {
		id, code, desc string
		line           int
	}
	rows, err := q.QueryContext(ctx, `SELECT purchase_order_item_id, item_code, description, line_number FROM purchase_order_items WHERE purchase_order_id=$1`, poID)
	if err != nil {
		return err
	}
	var pois []poi
	for rows.Next() {
		var p poi
		rows.Scan(&p.id, &p.code, &p.desc, &p.line)
		pois = append(pois, p)
	}
	rows.Close()
	type gri struct {
		id, code, desc string
		line           int
	}
	rows, err = q.QueryContext(ctx, `SELECT goods_receipt_item_id, item_code, description, line_number FROM goods_receipt_items WHERE goods_receipt_note_id=$1`, grnID)
	if err != nil {
		return err
	}
	var gris []gri
	for rows.Next() {
		var g gri
		rows.Scan(&g.id, &g.code, &g.desc, &g.line)
		gris = append(gris, g)
	}
	rows.Close()
	for _, g := range gris {
		best, bestScore := "", 0.0
		for _, p := range pois {
			sc := 0.0
			switch {
			case g.code != "" && util.NormalizeRef(g.code) == util.NormalizeRef(p.code):
				sc = 1
			default:
				sc = util.TextSimilarity(g.desc, p.desc)
				if p.code != "" && strings.Contains(util.NormalizeRef(g.desc), util.NormalizeRef(p.code)) {
					sc = 0.9
				}
				if sc < 0.5 && g.code == "" && g.line == p.line && len(gris) == len(pois) {
					sc = 0.5
				}
			}
			if sc > bestScore {
				best, bestScore = p.id, sc
			}
		}
		if bestScore >= 0.5 {
			if _, err := q.ExecContext(ctx, `UPDATE goods_receipt_items SET purchase_order_item_id=$2 WHERE goods_receipt_item_id=$1`, g.id, best); err != nil {
				return err
			}
		}
	}
	return nil
}

// relink connects GRNs to POs that arrived later and queues re-verification of open invoices,
// so documents can arrive in any order.
func (s *Service) relink(ctx context.Context) error {
	return db.InTx(ctx, s.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT g.goods_receipt_note_id, p.purchase_order_id FROM goods_receipt_notes g
			JOIN purchase_orders p ON p.po_number_normalized = regexp_replace(upper(g.po_reference), '[^A-Z0-9]', '', 'g')
			WHERE g.purchase_order_id IS NULL AND g.po_reference IS NOT NULL`)
		if err != nil {
			return err
		}
		var pairs [][2]string
		for rows.Next() {
			var a, b string
			rows.Scan(&a, &b)
			pairs = append(pairs, [2]string{a, b})
		}
		rows.Close()
		for _, p := range pairs {
			if err := s.linkGRN(ctx, tx, p[0], p[1]); err != nil {
				return err
			}
		}
		// tolerant pass: OCR slips in the GRN's PO reference, same vendor only
		rows, err = tx.QueryContext(ctx, `SELECT goods_receipt_note_id, po_reference, COALESCE(vendor_id::text,''), COALESCE(vendor_gstin,'')
			FROM goods_receipt_notes WHERE purchase_order_id IS NULL AND po_reference IS NOT NULL`)
		if err != nil {
			return err
		}
		type pending struct{ id, ref, vid, vg string }
		var ps []pending
		for rows.Next() {
			var p pending
			rows.Scan(&p.id, &p.ref, &p.vid, &p.vg)
			ps = append(ps, p)
		}
		rows.Close()
		for _, p := range ps {
			if poID, num, approx := findPOByRef(ctx, tx, p.ref, p.vid, p.vg); poID != "" {
				if err := s.linkGRN(ctx, tx, p.id, poID); err != nil {
					return err
				}
				if approx {
					s.event(ctx, tx, "", "", "GRN_LINKED_APPROX", fmt.Sprintf("GRN reference %s linked to PO %s (one character differs, likely OCR)", p.ref, num), "", nil)
				}
			}
		}
		// Re-evaluate every invoice still waiting on documents or review.
		_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs (job_type, invoice_id)
			SELECT 'VERIFY_INVOICE', i.invoice_id FROM invoices i
			WHERE i.invoice_status IN ('PENDING_DOCUMENTS','PENDING_PO_MATCH','PENDING_GRN','REVIEW_REQUIRED','ON_HOLD','READY_FOR_VERIFICATION')
			AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.job_type='VERIFY_INVOICE' AND j.invoice_id=i.invoice_id AND j.job_status='QUEUED')`)
		return err
	})
}
