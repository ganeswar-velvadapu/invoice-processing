package service

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// Background processing.
//
// A few goroutines (workers) take tasks from a channel: "read this document" or "re-check this invoice".
// The database is the record of unfinished work, so nothing is lost if the app stops halfway:
//   - a document waiting to be read keeps a pending processing_status;
//   - an invoice waiting for a re-check has verify_requested_at set.
// One dispatcher goroutine looks those up and sends them to the channel: right away when woken
// (after an upload, import or approval) and every 30 seconds, which also picks up work after a restart.

const (
	workerCount   = 2
	sweepInterval = 30 * time.Second
	taskTimeout   = 60 * time.Minute // a scanned page on the CPU vision model can take minutes
)

var pendingDocStatuses = `('RECEIVED','CLASSIFICATION_PENDING','EXTRACTION_PENDING')`

type task struct {
	documentID string // read this document
	invoiceID  string // or re-check this invoice
}

func (t task) key() string { return t.documentID + "|" + t.invoiceID }

// Start launches the background goroutines. They run for as long as the program runs; if it is stopped
// halfway, the unfinished work is still recorded in the database and is picked up on the next start.
func (s *Service) Start(database *sql.DB) {
	ctx := context.Background()
	tasks := make(chan task)
	for i := 0; i < workerCount; i++ {
		go s.worker(ctx, database, tasks)
	}
	go s.dispatch(ctx, database, tasks)
	go s.RunEmailPoller(ctx, database)
	slog.Info("background processing started", "workers", workerCount)
}

// wake carries "there is new work" signals to the dispatcher (holds at most one pending signal).
var wake = make(chan struct{}, 1)

// wakeUp asks the dispatcher to look for new work now instead of at the next sweep.
// Call it after the database transaction that created the work has committed.
func wakeUp() {
	select {
	case wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

func (s *Service) dispatch(ctx context.Context, database *sql.DB, tasks chan<- task) {
	tick := time.NewTicker(sweepInterval)
	defer tick.Stop()
	for {
		for _, t := range s.pendingTasks(ctx, database) {
			if !markBusy(t) {
				continue // already being worked on
			}
			select {
			case tasks <- t:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-tick.C:
		}
	}
}

func (s *Service) pendingTasks(ctx context.Context, database *sql.DB) []task {
	var out []task
	rows, err := database.QueryContext(ctx, `SELECT document_id::text, '' FROM documents WHERE processing_status IN `+pendingDocStatuses+`
		UNION ALL
		SELECT '', invoice_id::text FROM invoices WHERE verify_requested_at IS NOT NULL
		LIMIT 200`)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("look up pending work", "err", err)
		}
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var t task
		if rows.Scan(&t.documentID, &t.invoiceID) == nil {
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) worker(ctx context.Context, database *sql.DB, tasks <-chan task) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tasks:
			s.run(ctx, database, t)
			markDone(t)
			wakeUp() // finishing a document can create new work (e.g. a PO that unblocks invoices)
		}
	}
}

func (s *Service) run(ctx context.Context, database *sql.DB, t task) {
	tctx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()
	if t.documentID != "" {
		err := s.ProcessDocument(tctx, database, t.documentID)
		if err != nil && ctx.Err() == nil { // on shutdown the document stays pending and is redone on start
			slog.Error("document processing failed", "document", t.documentID, "err", err)
			database.ExecContext(ctx, `UPDATE documents SET processing_status='FAILED', processing_error=$2, processing_stage=NULL, updated_at=now()
				WHERE document_id=$1`, t.documentID, err.Error())
			s.event(ctx, database, "", t.documentID, "PROCESSING_FAILED", "Processing failed: "+err.Error(), "", nil)
		}
		return
	}
	var requested sql.NullTime
	database.QueryRowContext(ctx, `SELECT verify_requested_at FROM invoices WHERE invoice_id=$1`, t.invoiceID).Scan(&requested)
	if !requested.Valid {
		return
	}
	if _, err := s.Verify(tctx, database, t.invoiceID, ""); err != nil && ctx.Err() == nil {
		slog.Error("invoice re-check failed", "invoice", t.invoiceID, "err", err)
	}
	if ctx.Err() == nil {
		// clear only the request we handled; a newer request made meanwhile stays set
		database.ExecContext(ctx, `UPDATE invoices SET verify_requested_at=NULL WHERE invoice_id=$1 AND verify_requested_at <= $2`,
			t.invoiceID, requested.Time)
	}
}

// busy holds the tasks currently with a worker, so the dispatcher never sends the same one twice.
var busy = struct {
	mu sync.Mutex
	m  map[string]bool
}{m: map[string]bool{}}

func markBusy(t task) bool {
	busy.mu.Lock()
	defer busy.mu.Unlock()
	if busy.m[t.key()] {
		return false
	}
	busy.m[t.key()] = true
	return true
}

func markDone(t task) {
	busy.mu.Lock()
	delete(busy.m, t.key())
	busy.mu.Unlock()
}
