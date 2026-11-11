// Package service is the application core: the common ingestion service, the processing pipeline
// (extract → classify → parse → materialise → verify), procurement linking and AP actions.
// Every channel (manual upload now; email/ERP/API later) enters through Ingest.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"invoiceproc/internal/config"
	"invoiceproc/internal/db"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/model"
	"invoiceproc/internal/storage"
)

type Service struct {
	DB        *sql.DB
	Store     *storage.FS
	Extractor *extract.Extractor
	VLM       *extract.VLM // nil when EXTRACTION_ENGINE=rules
	Cfg       *config.Config
	Log       *slog.Logger
}

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrForbidden   = errors.New("forbidden")
	ErrInvalid     = errors.New("invalid request")
	ErrUnsupported = errors.New("unsupported file type")
	ErrTooLarge    = errors.New("file too large")
)

// UserError carries a message safe to show to API clients.
type UserError struct {
	Kind error
	Msg  string
}

func (e *UserError) Error() string { return e.Msg }
func (e *UserError) Unwrap() error { return e.Kind }

func userErr(kind error, msg string) error { return &UserError{kind, msg} }

var typeMap = pgtype.NewMap()

// textArray returns a scanner for text[] columns.
func textArray(dst *[]string) sql.Scanner { return typeMap.SQLScanner(dst) }

func jsonb(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Event writes an audit event. invoiceID/documentID may be empty.
func (s *Service) event(ctx context.Context, q db.Querier, invoiceID, documentID, typ, desc, actorID string, data any) {
	actorType := "SYSTEM"
	if actorID != "" {
		actorType = "USER"
	}
	var payload any
	if data != nil {
		payload = jsonb(data)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO invoice_processing_events
		(invoice_id, document_id, event_type, event_description, actor_type, actor_id, event_data)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`,
		nullable(invoiceID), nullable(documentID), typ, desc, actorType, nullable(actorID), payload); err != nil {
		s.Log.Error("audit event failed", "type", typ, "err", err)
	}
}

func (s *Service) ActiveTolerance(ctx context.Context, q db.Querier) (model.Tolerance, error) {
	var t model.Tolerance
	err := q.QueryRowContext(ctx, `SELECT version, price_tolerance_pct, price_tolerance_abs, quantity_tolerance_pct,
		tax_rounding_abs, date_tolerance_days, min_extraction_confidence FROM tolerance_rules WHERE is_active`).
		Scan(&t.Version, &t.PricePct, &t.PriceAbs, &t.QuantityPct, &t.TaxRoundingAbs, &t.DateToleranceDays, &t.MinExtractionConfidence)
	return t, err
}
