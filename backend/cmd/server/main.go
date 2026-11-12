// Command server is the single backend binary. Modes:
//
//	server api      HTTP API (runs migrations on start)
//	server worker   background document-processing worker
//	server all      API + worker in one process (local development)
//	server migrate  apply migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"invoiceproc/internal/api"
	"invoiceproc/internal/auth"
	"invoiceproc/internal/config"
	"invoiceproc/internal/db"
	"invoiceproc/internal/extract"
	"invoiceproc/internal/gstin"
	"invoiceproc/internal/service"
	"invoiceproc/internal/storage"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	mode := "all"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if err := run(mode, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(mode string, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gstin.SetChecksumMode(cfg.GSTINChecksum)
	if gstin.ChecksumMode() != "strict" {
		log.Warn("GSTIN checksum not enforced", "mode", gstin.ChecksumMode())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	if mode == "migrate" || mode == "api" || mode == "all" {
		if err := db.Migrate(d); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations applied")
		if mode == "migrate" {
			return nil
		}
	}
	store, err := storage.New(cfg.StorageDir)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	svc := &service.Service{DB: d, Store: store, Cfg: cfg, Log: log,
		Extractor: &extract.Extractor{PdftotextBin: cfg.PdftotextBin, MinPageChars: 40}}
	if cfg.ExtractionEngine != "rules" {
		svc.VLM = &extract.VLM{URL: cfg.OllamaURL, Model: cfg.VLMModel, PageTimeout: cfg.VLMPageTimeout, MaxPx: cfg.VLMMaxPx,
			PdftoppmBin: cfg.PdftoppmBin}
		log.Info("vision model extraction", "engine", cfg.ExtractionEngine, "model", cfg.VLMModel, "url", cfg.OllamaURL)
	}
	authSvc := &auth.Service{DB: d, Secret: []byte(cfg.JWTSecret), TTL: cfg.JWTTTL}

	switch mode {
	case "worker":
		log.Info("waiting for database schema")
		if err := db.WaitForSchema(ctx, d, 3*time.Minute); err != nil {
			return err
		}
		log.Info("worker started", "concurrency", cfg.WorkerConcurrency, "storage", store.Root)
		go svc.RunEmailPoller(ctx)
		svc.RunWorker(ctx)
		return nil
	case "api", "all":
		created, err := authSvc.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword)
		if err != nil {
			return err
		}
		if created {
			log.Info("bootstrap admin created", "email", cfg.AdminEmail)
		}
		if mode == "all" || cfg.RunWorkerInAPI {
			go svc.RunWorker(ctx)
			go svc.RunEmailPoller(ctx)
			log.Info("in-process worker started")
		}
		a := &api.API{Svc: svc, Auth: authSvc, DB: d, Log: log}
		srv := &http.Server{Addr: cfg.ListenAddr, Handler: a.Router(cfg.CORSOrigins), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 2 * time.Minute, WriteTimeout: 5 * time.Minute}
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			srv.Shutdown(sctx)
		}()
		log.Info("api listening", "addr", cfg.ListenAddr, "storage", store.Root)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown mode %q (api, worker, all, migrate)", mode)
}
