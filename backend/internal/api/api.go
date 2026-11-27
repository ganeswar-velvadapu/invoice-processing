// Package api exposes the HTTP endpoints. Handlers are thin: writes go through the service layer,
// reads are SQL queries that build JSON directly in PostgreSQL.
package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/config"
	"invoiceproc/internal/handlers"
	"invoiceproc/internal/service"
	"invoiceproc/internal/utils"
)

// RunApp sets up the app, builds the router and starts the HTTP server.
// It blocks while the server is running.
func RunApp(cfg *config.Config, database *sql.DB) error {
	app, err := service.New(cfg) // file storage, PDF reader, vision model client
	if err != nil {
		return err
	}
	app.Start(database) // background goroutines: read documents, re-check invoices, poll email

	router, err := Router(app, database, cfg)
	if err != nil {
		return err
	}
	slog.Info("api listening", "addr", cfg.ListenAddr)
	return http.ListenAndServe(cfg.ListenAddr, router)
}

// Router creates the first admin when there are no users yet and registers every route.
func Router(app *service.Service, database *sql.DB, cfg *config.Config) (*gin.Engine, error) {
	created, err := auth.EnsureAdmin(context.Background(), database, cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		return nil, err
	}
	if created {
		slog.Info("first admin created", "email", cfg.AdminEmail)
	}
	h := &handlers.Handler{Svc: app, DB: database}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), utils.Logger(), utils.CORS(cfg.CORSOrigins))
	r.MaxMultipartMemory = 8 << 20
	r.GET("/healthz", func(c *gin.Context) {
		if err := database.PingContext(c); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	g := r.Group("/api")
	g.POST("/auth/login", h.Login)

	p := g.Group("", auth.RequireLogin(database, []byte(cfg.JWTSecret))) // everything below needs a login
	// Who may do what. Routes without one of these are open to every logged-in user.
	managers := auth.RequireRole(auth.RoleManager, auth.RoleAdmin)
	admins := auth.RequireRole(auth.RoleAdmin)

	p.GET("/auth/me", h.Me)
	p.GET("/users", admins, h.ListUsers)
	p.POST("/users", admins, h.CreateUser)
	p.PATCH("/users/:id", admins, h.UpdateUser)

	p.GET("/dashboard/summary", h.Summary)

	p.POST("/documents", h.UploadDocument)
	p.GET("/documents", h.ListDocuments)
	p.GET("/email/status", h.EmailStatus)
	p.GET("/email/messages", h.ListEmails)
	p.GET("/documents/:id", h.GetDocument)
	p.GET("/documents/:id/file", h.DocumentFile)
	p.POST("/documents/:id/process", h.Reprocess)
	p.GET("/documents/:id/extraction", h.GetExtraction)
	p.PUT("/documents/:id/extraction", h.CorrectExtraction)

	p.GET("/vendors", h.ListVendors)
	p.POST("/vendors", managers, h.CreateVendor)
	p.PUT("/vendors/:id/profile", admins, h.SetVendorProfile)

	p.GET("/purchase-orders", h.ListPOs)
	p.POST("/purchase-orders", managers, h.CreatePO)
	p.GET("/purchase-orders/:id", h.GetPO)
	p.GET("/goods-receipt-notes", h.ListGRNs)
	p.POST("/goods-receipt-notes", managers, h.CreateGRN)
	p.GET("/goods-receipt-notes/:id", h.GetGRN)
	p.POST("/imports/procurement", managers, h.ImportProcurement)
	p.GET("/imports/template", h.ImportTemplate)

	p.GET("/invoices", h.ListInvoices)
	p.GET("/invoices/:id", h.GetInvoice)
	p.POST("/invoices/:id/verify", h.Verify)
	p.GET("/invoices/:id/checks", h.InvoiceChecks)
	p.GET("/invoices/:id/exceptions", h.InvoiceExceptions)
	p.GET("/invoices/:id/events", h.InvoiceEvents)
	p.GET("/invoices/:id/comparison", h.Comparison)
	p.POST("/invoices/:id/po-match", h.MatchPO)
	p.POST("/invoices/:id/approve", managers, h.Approve)
	p.POST("/invoices/:id/reject", managers, h.Reject)
	p.POST("/invoices/:id/override", managers, h.Override)
	p.POST("/invoices/:id/return", managers, h.ReturnToClerk)
	p.POST("/invoices/:id/resubmit", h.Resubmit)
	p.POST("/invoices/:id/submit", h.SubmitForApproval)

	p.GET("/exceptions", h.ListExceptions)
	p.POST("/exceptions/:id", h.UpdateException)

	p.GET("/tolerance-rules", h.ListTolerances)
	p.POST("/tolerance-rules", admins, h.CreateTolerance)
	return r, nil
}
