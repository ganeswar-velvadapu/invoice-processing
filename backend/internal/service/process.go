package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"invoiceproc/internal/classify"
	"invoiceproc/internal/db"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/parse"
	"invoiceproc/internal/utils"
)

type docRow struct {
	id, path, mime, sha string
	declared, dupOf     sql.NullString
	locked              bool
}

func (s *Service) setStage(ctx context.Context, database *sql.DB, docID, status, stage string) {
	database.ExecContext(ctx, `UPDATE documents SET processing_status=$2, processing_stage=$3, updated_at=now() WHERE document_id=$1`, docID, status, stage)
}

// ProcessDocument reads one stored document and saves what it found (invoice, PO or GRN):
//  1. a digital PDF is read from its own text by rules, and kept only if its numbers reconcile;
//  2. otherwise (scans, handwriting, or rules unsure) the local vision model reads it.
func (s *Service) ProcessDocument(ctx context.Context, database *sql.DB, docID string) error {
	var d docRow
	err := database.QueryRowContext(ctx, `SELECT document_id, storage_path, mime_type, sha256_hash, declared_document_type, duplicate_of_document_id,
		document_type_locked FROM documents WHERE document_id=$1`, docID).Scan(&d.id, &d.path, &d.mime, &d.sha, &d.declared, &d.dupOf, &d.locked)
	if err != nil {
		return fmt.Errorf("load document: %w", err)
	}
	full, err := s.Store.Path(d.path)
	if err != nil {
		return err
	}
	return s.readDocument(ctx, database, d, full)
}

func lineTexts(ls []extract.Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Text
	}
	return out
}

// applyType reconciles the detected type with the uploader's declared type, stores it and logs it.
func (s *Service) applyType(ctx context.Context, database *sql.DB, d docRow, detected string, conf float64, pages int, via string, evidence map[string]any) (string, float64, error) {
	docType, typeConf := detected, conf
	var note string
	if declared := d.declared.String; d.declared.Valid && declared != "" {
		switch {
		case declared == detected:
			typeConf = max(typeConf, 0.95) // user and content agree
		case d.locked:
			docType, typeConf = declared, 1
			note = fmt.Sprintf("Type %s set by a reviewer (content suggested %s)", declared, detected)
		case detected != "OTHER" && conf >= 0.85:
			note = fmt.Sprintf("Declared type %s overridden: the document's title indicates %s", declared, detected)
		default:
			docType, typeConf = declared, 0.9
			note = fmt.Sprintf("Using declared type %s (content suggested %s at %.0f%%)", declared, detected, conf*100)
		}
	}
	if _, err := database.ExecContext(ctx, `UPDATE documents SET document_type=$2, classification_confidence=$3, page_count=$4, updated_at=now() WHERE document_id=$1`,
		d.id, docType, typeConf, pages); err != nil {
		return "", 0, err
	}
	desc := fmt.Sprintf("Classified as %s (%.0f%%) using %s", docType, typeConf*100, via)
	if docType == "OTHER" {
		desc += ". Document type could not be determined; please choose it in review"
	}
	if note != "" {
		desc += ". " + note
	}
	s.event(ctx, database, "", d.id, "CLASSIFIED", desc, "", evidence)
	return docType, typeConf, nil
}

// readDocument tries the PDF's own text with rules first, then the vision model. The text layer is
// also used to verify the model's values.
func (s *Service) readDocument(ctx context.Context, database *sql.DB, d docRow, full string) error {
	s.setStage(ctx, database, d.id, "EXTRACTION_PENDING", "reading text layer")
	tl, err := s.Extractor.Extract(ctx, full, d.mime)
	if err != nil {
		return fmt.Errorf("text layer: %w", err)
	}
	texts := lineTexts(tl.Lines)
	fullTextLayer := tl.FullTextLayer()

	// fast path: digital PDFs whose critical fields the rules read confidently
	var rulesData *parse.Data // kept in case the vision model is unavailable
	var rulesType string
	var rulesConf float64
	if fullTextLayer {
		cls := classify.Classify(texts)
		if cls.DocumentType != "OTHER" {
			tol, err := s.ActiveTolerance(ctx, database)
			if err != nil {
				return err
			}
			data := s.parseWithProfile(ctx, database, tl.Lines, cls.DocumentType)
			data.ClassificationConfidence = cls.Confidence
			s.boostWithMasterData(ctx, database, data)
			low := parse.LowConfidence(data, tol.MinExtractionConfidence)
			problems := parse.ReconcileProblems(data, tl.Lines)
			if len(low) == 0 && len(problems) == 0 {
				docType, typeConf, err := s.applyType(ctx, database, d, cls.DocumentType, cls.Confidence, tl.PageCount, "text layer + rules",
					map[string]any{"classification": cls, "engine": "rules"})
				if err != nil {
					return err
				}
				if docType != cls.DocumentType {
					data = s.parseWithProfile(ctx, database, tl.Lines, docType)
				}
				data.ClassificationConfidence = typeConf
				data.ExtractedBy = "rules"
				full := 100.0
				return s.saveAndMaterialize(ctx, database, d.id, docType, extract.MethodPDFText, tl.Text(), data, &full, "")
			} else {
				reasons := append(append([]string{}, problems...), func() []string {
					if len(low) > 0 {
						return []string{"low confidence: " + strings.Join(low, ", ")}
					}
					return nil
				}()...)
				rulesData, rulesType, rulesConf = data, cls.DocumentType, cls.Confidence
				s.event(ctx, database, "", d.id, "RULES_UNSURE", "Rules reading did not reconcile ("+strings.Join(reasons, "; ")+"); sending to the vision model", "",
					map[string]any{"problems": problems, "low_confidence": low})
			}
		}
	}

	// vision model
	s.setStage(ctx, database, d.id, "EXTRACTION_PENDING", "vision model reading the document")
	hint := ""
	if d.declared.Valid {
		hint = d.declared.String
	}
	vd, err := s.VLM.Extract(ctx, full, d.mime, hint)
	if err != nil {
		if rulesData == nil || ctx.Err() != nil {
			return err // already reads "vision model, page N: ..."
		}
		// Vision model unavailable: keep the rules reading, but send it to a person to confirm.
		slog.Warn("vision model unavailable, saving rules reading for review", "document", d.id, "err", err)
		docType, typeConf, terr := s.applyType(ctx, database, d, rulesType, rulesConf, tl.PageCount, "text layer + rules (vision model unavailable)",
			map[string]any{"engine": "rules", "vision_error": err.Error()})
		if terr != nil {
			return terr
		}
		for _, f := range rulesData.Fields {
			f.Confidence = min(f.Confidence, 0.5)
		}
		rulesData.Warnings = append(rulesData.Warnings, "vision model unavailable; the rules reading did not reconcile, so please check it against the original")
		rulesData.ClassificationConfidence = typeConf
		rulesData.ExtractedBy = "rules"
		return s.saveAndMaterialize(ctx, database, d.id, docType, extract.MethodPDFText, tl.Text(), rulesData, nil, "")
	}
	detected, conf := vd.DocumentType, 0.9
	if detected == "" {
		detected, conf = "OTHER", 0.3
	}
	if len(texts) > 0 { // a second opinion from the text layer's title
		if cls := classify.Classify(texts); cls.DocumentType == detected {
			conf = 0.99
		} else if cls.DocumentType != "OTHER" && cls.Confidence >= 0.85 {
			conf = 0.6 // model and document title disagree: let a person confirm the type
		}
	}
	docType, typeConf, err := s.applyType(ctx, database, d, detected, conf, max(vd.Pages, tl.PageCount), "vision model "+s.VLM.Model,
		map[string]any{"engine": "vlm", "provider": s.VLM.Provider, "model": s.VLM.Model, "model_type": vd.DocumentType, "seconds": int(vd.Duration.Seconds())})
	if err != nil {
		return err
	}
	opt := parse.Options{BuyerGSTINs: s.Cfg.BuyerGSTINs, VendorGSTINs: s.vendorGSTINs(ctx, database)}
	data := parse.FromVLM(vd, docType, tl.Lines, opt)
	data.ClassificationConfidence = typeConf
	data.ExtractedBy = "vlm:" + s.VLM.Model
	if s.VLM.Provider == "cloud" {
		data.ExtractedBy = "vlm:cloud:" + s.VLM.Model // shown on the document page, so it is clear the file left this machine
	}
	return s.saveAndMaterialize(ctx, database, d.id, docType, "VLM", tl.Text(), data, nil, "")
}

func (s *Service) parseWithProfile(ctx context.Context, database *sql.DB, lines []extract.Line, docType string) *parse.Data {
	opt := parse.Options{BuyerGSTINs: s.Cfg.BuyerGSTINs, VendorGSTINs: s.vendorGSTINs(ctx, database)}
	data := parse.Parse(lines, docType, opt)
	g := ""
	for _, k := range []string{"seller_gstin", "vendor_gstin"} {
		if f := data.Fields[k]; f != nil {
			g = f.Value
		}
	}
	if g == "" {
		return data
	}
	var raw []byte
	err := database.QueryRowContext(ctx, `SELECT p.field_patterns FROM vendor_extraction_profiles p JOIN vendors v USING (vendor_id)
		WHERE v.gstin=$1 AND p.document_type=$2 AND p.is_active`, g, docType).Scan(&raw)
	if err != nil {
		return data
	}
	var prof parse.Profile
	if json.Unmarshal(raw, &prof) == nil && len(prof) > 0 {
		opt.Profile = prof
		data = parse.Parse(lines, docType, opt)
	}
	return data
}

// saveAndMaterialize stores a new extraction version, then creates/updates the business record
// and runs verification or relinking. Used by the pipeline and by manual corrections.
func (s *Service) saveAndMaterialize(ctx context.Context, database *sql.DB, docID, docType, method, rawText string, data *parse.Data, ocrConf *float64, userID string) error {
	tol, err := s.ActiveTolerance(ctx, database)
	if err != nil {
		return err
	}
	s.boostWithMasterData(ctx, database, data)
	overall := parse.OverallConfidence(data)
	low := parse.LowConfidence(data, tol.MinExtractionConfidence)
	if method == "MANUAL" {
		low = nil
	}
	if docType == "PO" || docType == "GRN" {
		// POs and GRNs are master data from procurement / stores. One coming from an AP clerk (who also handles
		// the invoices checked against it) only becomes a record after a manager confirms it.
		// The same applies to one that arrived by email: anyone can send an email.
		actor, source := userID, ""
		database.QueryRowContext(ctx, `SELECT COALESCE(uploaded_by_user_id::text,''), source_type FROM documents WHERE document_id=$1`, docID).Scan(&actor, &source)
		if userID != "" {
			actor = userID
		}
		if userID == "" && source == "EMAIL" {
			low = append(low, "manager_confirmation")
		} else if actor != "" && s.userRole(ctx, database, actor) == "AP_CLERK" {
			if userID != "" {
				return utils.UserErr(utils.ErrForbidden, "only an AP manager or admin can confirm purchase orders and goods receipts")
			}
			low = append(low, "manager_confirmation")
		}
	}
	if low == nil {
		low = []string{}
	}
	var invoiceID string
	var poIDs []string
	err = db.InTx(ctx, database, func(tx *sql.Tx) error {
		var ver int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0)+1 FROM document_extractions WHERE document_id=$1`, docID).Scan(&ver); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_extractions (document_id, version, extraction_method, raw_text, extracted_data,
			overall_confidence, ocr_mean_confidence, low_confidence_fields, created_by_user_id, extraction_model) VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10)`,
			docID, ver, method, rawText, jsonb(data), overall, ocrConf, low, nullable(userID), nullable(data.ExtractedBy)); err != nil {
			return err
		}
		evType, evDesc := "EXTRACTED", fmt.Sprintf("Extraction v%d (%s): %d fields, %d line items, confidence %.2f", ver, method, len(data.Fields), len(data.Items), overall)
		if userID != "" {
			evType, evDesc = "EXTRACTION_CORRECTED", fmt.Sprintf("Extraction corrected manually (v%d)", ver)
		}
		if len(low) > 0 {
			evDesc += "; needs review: " + strings.Join(low, ", ")
		}
		s.event(ctx, tx, "", docID, evType, evDesc, userID, map[string]any{"version": ver, "low_confidence_fields": low, "warnings": data.Warnings})

		switch docType {
		case "INVOICE":
			id, err := s.materializeInvoice(ctx, tx, docID, data, overall, userID)
			if err != nil {
				return err
			}
			invoiceID = id
		case "PO", "GRN":
			// Master data must not be guessed: an uncertain PO/GRN extraction waits in review and only
			// becomes a record once its number, lines and parties are confident or confirmed by a person.
			if len(low) > 0 {
				if err := s.dropStaleDocumentRecords(ctx, tx, docID, docType, ""); err != nil {
					return err
				}
				s.event(ctx, tx, "", docID, "RECORD_PENDING_REVIEW",
					fmt.Sprintf("%s record not created yet: confirm %s in extraction review", docType, strings.Join(low, ", ")), "", nil)
				break
			}
			if docType == "PO" {
				in, err := poInputFromData(data, docID)
				if err != nil {
					s.event(ctx, tx, "", docID, "RECORD_NOT_CREATED", err.Error(), "", nil)
					break
				}
				if err := s.dropStaleDocumentRecords(ctx, tx, docID, docType, in.PONumber); err != nil {
					return err
				}
				id, err := s.upsertPO(ctx, tx, in, userID)
				if err != nil {
					return err
				}
				poIDs = append(poIDs, id)
			} else {
				in, err := grnInputFromData(data, docID)
				if err != nil {
					s.event(ctx, tx, "", docID, "RECORD_NOT_CREATED", err.Error(), "", nil)
					break
				}
				if err := s.dropStaleDocumentRecords(ctx, tx, docID, docType, in.GRNNumber); err != nil {
					return err
				}
				if _, err := s.upsertGRN(ctx, tx, in, userID); err != nil {
					return err
				}
			}
		}
		status := "PROCESSED"
		if len(low) > 0 {
			status = "EXTRACTION_REVIEW"
		} else if docType == "INVOICE" {
			status = "VALIDATION_PENDING"
		}
		_, err := tx.ExecContext(ctx, `UPDATE documents SET processing_status=$2, processing_stage=NULL, processing_error=NULL, updated_at=now() WHERE document_id=$1`, docID, status)
		return err
	})
	if err != nil {
		return err
	}
	if invoiceID != "" {
		_, err = s.Verify(ctx, database, invoiceID, userID)
		return err
	}
	if docType == "PO" || docType == "GRN" {
		return s.relink(ctx, database)
	}
	return nil
}

// boostWithMasterData raises GSTIN confidence when it exactly matches a vendor master record:
// an independent confirmation that the extracted value is right.
func (s *Service) boostWithMasterData(ctx context.Context, database *sql.DB, data *parse.Data) {
	for _, k := range []string{"seller_gstin", "vendor_gstin"} {
		f := data.Fields[k]
		if f == nil || f.Confidence >= 0.95 {
			continue
		}
		var n int
		if database.QueryRowContext(ctx, `SELECT count(*) FROM vendors WHERE gstin=$1`, f.Value).Scan(&n) == nil && n > 0 {
			f.Confidence = 0.95
			f.Source += "+vendor_master"
		}
	}
}

// CorrectExtraction saves user-corrected data as a new MANUAL extraction version and re-runs downstream steps.
func (s *Service) CorrectExtraction(ctx context.Context, database *sql.DB, docID string, data *parse.Data, userID string) error {
	var docType, raw string
	err := database.QueryRowContext(ctx, `SELECT d.document_type, COALESCE(e.raw_text,'') FROM documents d
		LEFT JOIN LATERAL (SELECT raw_text FROM document_extractions WHERE document_id=d.document_id ORDER BY version DESC LIMIT 1) e ON true
		WHERE d.document_id=$1`, docID).Scan(&docType, &raw)
	if err == sql.ErrNoRows {
		return utils.ErrNotFound
	}
	if err != nil {
		return err
	}
	if data.DocumentType != "" && data.DocumentType != docType {
		if !allowedTypes[data.DocumentType] {
			return utils.UserErr(utils.ErrInvalid, "unknown document_type")
		}
		docType = data.DocumentType
		if _, err := database.ExecContext(ctx, `UPDATE documents SET document_type=$2 WHERE document_id=$1`, docID, docType); err != nil {
			return err
		}
	}
	data.DocumentType = docType
	data.ClassificationConfidence = 1 // confirmed by a person
	if data.Fields == nil {
		data.Fields = map[string]*parse.Field{}
	}
	// Previous version tells which values the user actually changed.
	var prevRaw []byte
	prev := &parse.Data{}
	if database.QueryRowContext(ctx, `SELECT extracted_data FROM document_extractions WHERE document_id=$1 ORDER BY version DESC LIMIT 1`, docID).Scan(&prevRaw) == nil {
		json.Unmarshal(prevRaw, prev)
	}
	for k, f := range data.Fields {
		if f == nil {
			delete(data.Fields, k)
			continue
		}
		f.Value = strings.TrimSpace(f.Value)
		if f.Value == "" {
			delete(data.Fields, k)
			continue
		}
		// Everything the reviewer submits has been looked at by a human.
		if p := prev.Fields[k]; p == nil || p.Value != f.Value {
			f.Source = "manual"
		}
		f.Confidence = 1
	}
	for i := range data.Items {
		data.Items[i].LineNumber = i + 1
		data.Items[i].Confidence = 1
		if data.Items[i].Source == "" {
			data.Items[i].Source = "manual"
		}
	}
	return s.saveAndMaterialize(ctx, database, docID, docType, "MANUAL", raw, data, nil, userID)
}

// vendorGSTINs returns the vendor master GSTINs used to repair OCR-damaged GSTINs.
func (s *Service) vendorGSTINs(ctx context.Context, database *sql.DB) []string {
	rows, err := database.QueryContext(ctx, `SELECT gstin FROM vendors WHERE gstin IS NOT NULL`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			out = append(out, g)
		}
	}
	return out
}

// dropStaleDocumentRecords removes a PO/GRN that an earlier (wrong) extraction of this document created,
// e.g. a PO saved as "WZ-202" before the reviewer corrected it to "PUR-2024-00004". Records that are
// already referenced (matched invoices, linked GRNs) are kept and reported instead of deleted.
func (s *Service) dropStaleDocumentRecords(ctx context.Context, tx *sql.Tx, docID, docType, keepNumber string) error {
	keep := utils.NormalizeRef(keepNumber)
	switch docType {
	case "PO":
		rows, err := tx.QueryContext(ctx, `SELECT purchase_order_id, po_number,
			EXISTS (SELECT 1 FROM invoice_po_matches m WHERE m.purchase_order_id=p.purchase_order_id)
			OR EXISTS (SELECT 1 FROM grn_po_matches g WHERE g.purchase_order_id=p.purchase_order_id)
			FROM purchase_orders p WHERE document_id=$1 AND source='DOCUMENT' AND po_number_normalized<>$2`, docID, keep)
		if err != nil {
			return err
		}
		type rec struct {
			id, num string
			used    bool
		}
		var recs []rec
		for rows.Next() {
			var r rec
			rows.Scan(&r.id, &r.num, &r.used)
			recs = append(recs, r)
		}
		rows.Close()
		for _, r := range recs {
			if r.used {
				s.event(ctx, tx, "", docID, "STALE_RECORD_KEPT", fmt.Sprintf("PO %s from an earlier extraction is already matched; review it manually", r.num), "", nil)
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM purchase_orders WHERE purchase_order_id=$1`, r.id); err != nil {
				return err
			}
			s.event(ctx, tx, "", docID, "STALE_RECORD_REMOVED", fmt.Sprintf("Removed PO %s created by an earlier extraction of this document", r.num), "", nil)
		}
	case "GRN":
		res, err := tx.ExecContext(ctx, `DELETE FROM goods_receipt_notes WHERE document_id=$1 AND source='DOCUMENT' AND grn_number_normalized<>$2`, docID, keep)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			s.event(ctx, tx, "", docID, "STALE_RECORD_REMOVED", "Removed a GRN created by an earlier extraction of this document", "", nil)
		}
	}
	return nil
}

func (s *Service) userRole(ctx context.Context, database *sql.DB, userID string) string {
	var role string
	database.QueryRowContext(ctx, `SELECT role FROM users WHERE user_id=$1`, userID).Scan(&role)
	return role
}
