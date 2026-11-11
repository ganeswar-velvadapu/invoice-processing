// Package db opens the PostgreSQL connection and runs embedded migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"invoiceproc/migrations"
)

func Open(ctx context.Context, url string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Exec mode uses unnamed statements, so it works through PgBouncer-style poolers such as
	// Neon's "-pooler" endpoint (named prepared statements collide across pooled sessions).
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	d := stdlib.OpenDB(*cfg)
	d.SetMaxOpenConns(15)
	d.SetMaxIdleConns(5)
	d.SetConnMaxIdleTime(4 * time.Minute) // Neon closes idle connections; recycle before that
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return d, nil
}

// Migrate applies all pending up migrations.
func Migrate(d *sql.DB) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	drv, err := pgxmigrate.WithInstance(d, &pgxmigrate.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// InTx runs fn inside a transaction.
func InTx(ctx context.Context, d *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// WaitForSchema blocks until the API has applied migrations (processing_jobs exists), so a worker
// started at the same time does not poll a missing table.
func WaitForSchema(ctx context.Context, d *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var reg sql.NullString
		if err := d.QueryRowContext(ctx, `SELECT to_regclass('public.processing_jobs')::text`).Scan(&reg); err == nil && reg.Valid {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("database schema not ready: start the api (it applies migrations) or run `server migrate`")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
