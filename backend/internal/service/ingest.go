package service

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"invoiceproc/internal/utils"
	"io"
	"os"
	"strings"

	"invoiceproc/internal/db"
)

// IngestRequest is the channel-independent input to the ingestion service.
type IngestRequest struct {
	Reader           io.Reader
	OriginalFilename string
	DeclaredType     string // optional user hint: PO, GRN, INVOICE, EWAY_BILL, MRN, OTHER
	SourceType       string // MANUAL (now); EMAIL, ERP, API later
	SourceReference  string // e.g. email message-id + attachment index
	EmailMessageID   string // email_messages row for documents that arrived by email
	UserID           string
}

type IngestResult struct {
	DocumentID            string `json:"document_id"`
	SHA256                string `json:"sha256_hash"`
	DuplicateOfDocumentID string `json:"duplicate_of_document_id,omitempty"`
	ProcessingStatus      string `json:"processing_status"`
}

var allowedTypes = map[string]bool{"PO": true, "GRN": true, "INVOICE": true, "EWAY_BILL": true, "MRN": true, "OTHER": true}

// sniff identifies supported files by magic bytes, never by the client's filename or header.
func sniff(head []byte) (mime, ext string) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf", ".pdf"
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", ".png"
	case bytes.HasPrefix(head, []byte("\xFF\xD8\xFF")):
		return "image/jpeg", ".jpg"
	case bytes.HasPrefix(head, []byte("II*\x00")), bytes.HasPrefix(head, []byte("MM\x00*")):
		return "image/tiff", ".tif"
	}
	return "", ""
}

// Ingest validates, hashes and stores a file, creates its document record and queues processing.
func (s *Service) Ingest(ctx context.Context, database *sql.DB, req IngestRequest) (*IngestResult, error) {
	if req.SourceType == "" {
		req.SourceType = "MANUAL"
	}
	req.DeclaredType = strings.ToUpper(strings.TrimSpace(req.DeclaredType))
	if req.DeclaredType != "" && !allowedTypes[req.DeclaredType] {
		return nil, utils.UserErr(utils.ErrInvalid, "unknown document_type "+req.DeclaredType)
	}
	br := bufio.NewReaderSize(io.LimitReader(req.Reader, s.Cfg.MaxUploadBytes+1), 4096)
	head, _ := br.Peek(16)
	if len(head) == 0 {
		return nil, utils.UserErr(utils.ErrInvalid, "file is empty")
	}
	mime, ext := sniff(head)
	if mime == "" {
		return nil, utils.UserErr(utils.ErrUnsupported, "unsupported file type: only PDF, PNG, JPEG and TIFF are accepted")
	}
	rel, size, sum, err := s.Store.Save(br, ext)
	if err != nil {
		return nil, fmt.Errorf("store file: %w", err)
	}
	if size > s.Cfg.MaxUploadBytes {
		if p, err := s.Store.Path(rel); err == nil {
			os.Remove(p)
		}
		return nil, utils.UserErr(utils.ErrTooLarge, fmt.Sprintf("file exceeds the %d MB limit", s.Cfg.MaxUploadBytes>>20))
	}
	name := req.OriginalFilename
	if len(name) > 255 {
		name = name[:255]
	}
	res := &IngestResult{SHA256: sum, ProcessingStatus: "CLASSIFICATION_PENDING"}
	err = db.InTx(ctx, database, func(tx *sql.Tx) error {
		var dup sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT document_id FROM documents WHERE sha256_hash=$1 ORDER BY created_at LIMIT 1`, sum).Scan(&dup); err != nil && err != sql.ErrNoRows {
			return err
		}
		res.DuplicateOfDocumentID = dup.String
		if err := tx.QueryRowContext(ctx, `INSERT INTO documents (document_type, declared_document_type, original_filename, storage_path,
			mime_type, file_size_bytes, sha256_hash, duplicate_of_document_id, source_type, source_reference, uploaded_by_user_id, processing_status, email_message_id)
			VALUES ('OTHER',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'CLASSIFICATION_PENDING',$11) RETURNING document_id`,
			nullable(req.DeclaredType), name, rel, mime, size, sum, nullable(dup.String), req.SourceType, nullable(req.SourceReference), nullable(req.UserID),
			nullable(req.EmailMessageID)).
			Scan(&res.DocumentID); err != nil {
			return err
		}
		desc := fmt.Sprintf("Received %q via %s (%s, %d bytes)", name, req.SourceType, mime, size)
		data := map[string]any{"sha256": sum, "source_reference": req.SourceReference, "declared_type": req.DeclaredType}
		if dup.Valid {
			desc += "; identical to an earlier document"
			data["duplicate_of_document_id"] = dup.String
		}
		s.event(ctx, tx, "", res.DocumentID, "DOCUMENT_RECEIVED", desc, req.UserID, data)
		return nil
	})
	if err != nil {
		if p, perr := s.Store.Path(rel); perr == nil {
			os.Remove(p)
		}
		return nil, err
	}
	wakeUp() // the document is saved as pending; a background worker reads it now
	return res, nil
}

// Reprocess reads the document again (optionally forcing a document type).
func (s *Service) Reprocess(ctx context.Context, database *sql.DB, documentID, forceType, userID string) error {
	forceType = strings.ToUpper(forceType)
	if forceType != "" && !allowedTypes[forceType] {
		return utils.UserErr(utils.ErrInvalid, "unknown document_type "+forceType)
	}
	defer wakeUp()
	return db.InTx(ctx, database, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE documents SET processing_status='CLASSIFICATION_PENDING', processing_error=NULL,
			declared_document_type=COALESCE($2, declared_document_type), document_type_locked=($2::text IS NOT NULL), updated_at=now()
			WHERE document_id=$1`, documentID, nullable(forceType))
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return utils.ErrNotFound
		}
		s.event(ctx, tx, "", documentID, "REPROCESS_REQUESTED", "Reprocessing requested", userID, map[string]any{"forced_type": forceType})
		return nil
	})
}
