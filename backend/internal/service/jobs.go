package service

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"invoiceproc/internal/db"
)

// enqueue adds a job; VERIFY_INVOICE jobs are de-duplicated while one is still queued.
func (s *Service) enqueue(ctx context.Context, q db.Querier, jobType, documentID, invoiceID string) error {
	if jobType == "VERIFY_INVOICE" {
		_, err := q.ExecContext(ctx, `INSERT INTO processing_jobs (job_type, invoice_id)
			SELECT 'VERIFY_INVOICE', $1::uuid WHERE NOT EXISTS (
			  SELECT 1 FROM processing_jobs WHERE job_type='VERIFY_INVOICE' AND invoice_id=$1::uuid AND job_status='QUEUED')`, invoiceID)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO processing_jobs (job_type, document_id) VALUES ($1,$2)`, jobType, documentID)
	return err
}

type job struct {
	id, typ               string
	documentID, invoiceID sql.NullString
	attempts, maxAttempts int
}

func (s *Service) claim(ctx context.Context) (*job, error) {
	j := &job{}
	err := s.DB.QueryRowContext(ctx, `UPDATE processing_jobs SET job_status='RUNNING', attempts=attempts+1, locked_at=now()
		WHERE processing_job_id = (
		  SELECT processing_job_id FROM processing_jobs
		  WHERE job_status='QUEUED' AND run_after <= now()
		  ORDER BY run_after FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING processing_job_id, job_type, document_id, invoice_id, attempts, max_attempts`).
		Scan(&j.id, &j.typ, &j.documentID, &j.invoiceID, &j.attempts, &j.maxAttempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err // never hand an empty job to the runner
	}
	return j, nil
}

func (s *Service) runJob(ctx context.Context, j *job) error {
	switch j.typ {
	case "PROCESS_DOCUMENT":
		return s.ProcessDocument(ctx, j.documentID.String)
	case "VERIFY_INVOICE":
		_, err := s.Verify(ctx, j.invoiceID.String, "")
		return err
	}
	return fmt.Errorf("unknown job type %s", j.typ)
}

func (s *Service) finish(ctx context.Context, j *job, runErr error) {
	if runErr == nil {
		s.DB.ExecContext(ctx, `UPDATE processing_jobs SET job_status='DONE', finished_at=now(), last_error=NULL WHERE processing_job_id=$1`, j.id)
		return
	}
	s.Log.Error("job failed", "job", j.id, "type", j.typ, "attempt", j.attempts, "err", runErr)
	if j.attempts < j.maxAttempts {
		backoff := time.Duration(j.attempts*j.attempts) * 10 * time.Second
		s.DB.ExecContext(ctx, `UPDATE processing_jobs SET job_status='QUEUED', last_error=$2, run_after=now()+$3::interval, locked_at=NULL
			WHERE processing_job_id=$1`, j.id, runErr.Error(), fmt.Sprintf("%d seconds", int(backoff.Seconds())))
		return
	}
	s.DB.ExecContext(ctx, `UPDATE processing_jobs SET job_status='FAILED', last_error=$2, finished_at=now() WHERE processing_job_id=$1`, j.id, runErr.Error())
	if j.documentID.Valid {
		s.DB.ExecContext(ctx, `UPDATE documents SET processing_status='FAILED', processing_error=$2, updated_at=now() WHERE document_id=$1`,
			j.documentID.String, runErr.Error())
		s.event(ctx, s.DB, "", j.documentID.String, "PROCESSING_FAILED", "Processing failed after retries: "+runErr.Error(), "", nil)
	}
}

// RunWorker processes jobs until ctx is cancelled.
func (s *Service) RunWorker(ctx context.Context) {
	var wg sync.WaitGroup
	workers := s.Cfg.WorkerConcurrency
	if s.VLM != nil && workers > 1 {
		workers = 1 // the vision model handles one page at a time; parallel jobs would only queue and time out
		s.Log.Info("vision model enabled: worker concurrency set to 1")
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for ctx.Err() == nil {
				j, err := s.claim(ctx)
				if err != nil {
					s.Log.Error("claim job", "err", err)
					select { // back off instead of spinning on a database problem
					case <-ctx.Done():
						return
					case <-time.After(5 * s.Cfg.WorkerPoll):
					}
					continue
				}
				if j == nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(s.Cfg.WorkerPoll):
					}
					continue
				}
				jctx, cancel := context.WithTimeout(context.Background(), s.Cfg.JobTimeout)
				err = s.runJob(jctx, j)
				s.finish(jctx, j, err)
				cancel()
			}
		}(i)
	}
	// recover jobs abandoned by a crashed worker
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET job_status='QUEUED', locked_at=NULL
					WHERE job_status='RUNNING' AND locked_at < now() - interval '15 minutes'`)
			}
		}
	}()
	wg.Wait()
}
