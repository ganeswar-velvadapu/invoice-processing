// Package service holds what the app does: taking in files (Ingest), reading them (ProcessDocument),
// checking invoices (Verify), procurement records, imports, approvals, email polling and the
// background goroutines (background.go). HTTP handlers call these functions.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"invoiceproc/internal/config"
	"invoiceproc/internal/db"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
	"invoiceproc/internal/model"
	"invoiceproc/internal/storage"
)

// Service holds the tools the app's functions use. The database connection is not here: the API
// owns it and passes it into each function that needs it.
type Service struct {
	Store     *storage.FS        // original files
	Extractor *extract.Extractor // reads a PDF's own text
	VLM       *extract.VLM       // local vision model (Ollama) for scans and unsure readings
	Cfg       *config.Config
}

// New sets up the tools.
func New(cfg *config.Config) (*Service, error) {
	store, err := storage.New(cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	gstin.SetChecksumMode(cfg.GSTINChecksum)
	if gstin.ChecksumMode() != "strict" {
		slog.Warn("GSTIN checksum not enforced", "mode", gstin.ChecksumMode())
	}
	return &Service{
		Store:     store,
		Extractor: &extract.Extractor{PdftotextBin: cfg.PdftotextBin, MinPageChars: 40},
		VLM: &extract.VLM{URL: cfg.OllamaURL, Model: cfg.VLMModel, PageTimeout: cfg.VLMPageTimeout, MaxPx: cfg.VLMMaxPx,
			PdftoppmBin: cfg.PdftoppmBin},
		Cfg: cfg,
	}, nil
}

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
		slog.Error("audit event failed", "type", typ, "err", err)
	}
}

func (s *Service) ActiveTolerance(ctx context.Context, q db.Querier) (model.Tolerance, error) {
	var t model.Tolerance
	err := q.QueryRowContext(ctx, `SELECT version, price_tolerance_pct, price_tolerance_abs, quantity_tolerance_pct,
		tax_rounding_abs, date_tolerance_days, min_extraction_confidence FROM tolerance_rules WHERE is_active`).
		Scan(&t.Version, &t.PricePct, &t.PriceAbs, &t.QuantityPct, &t.TaxRoundingAbs, &t.DateToleranceDays, &t.MinExtractionConfidence)
	return t, err
}
