// Command server runs the backend: it opens the database, applies migrations and runs the app.
package main

import (
	"context"
	"log/slog"
	"os"

	"invoiceproc/internal/api"
	"invoiceproc/internal/config"
	"invoiceproc/internal/db"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(log) // the one logger: every package logs with slog.Info / slog.Error

	cfg, err := config.Load() // settings from .env (the database URL is one of them)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	if err := db.Migrate(database); err != nil { // applies any new migrations/*.sql
		log.Error("migrations", "err", err)
		os.Exit(1)
	}

	if err := api.RunApp(cfg, database); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
