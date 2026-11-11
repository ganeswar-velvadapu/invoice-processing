// Package api exposes the HTTP endpoints. Handlers are thin: writes go through the service layer,
// reads are SQL queries that build JSON directly in PostgreSQL.
package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/service"
)

type API struct {
	Svc  *service.Service
	Auth *auth.Service
	DB   *sql.DB
	Log  *slog.Logger
}

func (a *API) Router(corsOrigins []string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), a.logger(), cors(corsOrigins))
	r.MaxMultipartMemory = 8 << 20
	r.GET("/healthz", func(c *gin.Context) {
		if err := a.DB.PingContext(c); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	g := r.Group("/api")
	g.POST("/auth/login", a.login)

	p := g.Group("", a.Auth.Middleware())
	clerk, manager, admin := auth.Require(auth.RoleClerk), auth.Require(auth.RoleManager), auth.Require(auth.RoleAdmin)
	p.Use(clerk)

	p.GET("/auth/me", a.me)
	p.GET("/users", admin, a.listUsers)
	p.POST("/users", admin, a.createUser)
	p.PATCH("/users/:id", admin, a.updateUser)

	p.GET("/dashboard/summary", a.summary)

	p.POST("/documents", a.uploadDocument)
	p.GET("/documents", a.listDocuments)
	p.GET("/email/status", a.emailStatus)
	p.GET("/email/messages", a.listEmails)
	p.GET("/documents/:id", a.getDocument)
	p.GET("/documents/:id/file", a.documentFile)
	p.POST("/documents/:id/process", a.reprocess)
	p.GET("/documents/:id/extraction", a.getExtraction)
	p.PUT("/documents/:id/extraction", a.correctExtraction)

	p.GET("/vendors", a.listVendors)
	p.POST("/vendors", manager, a.createVendor)
	p.PUT("/vendors/:id/profile", admin, a.setVendorProfile)

	p.GET("/purchase-orders", a.listPOs)
	p.POST("/purchase-orders", manager, a.createPO)
	p.GET("/purchase-orders/:id", a.getPO)
	p.GET("/goods-receipt-notes", a.listGRNs)
	p.POST("/goods-receipt-notes", manager, a.createGRN)
	p.GET("/goods-receipt-notes/:id", a.getGRN)
	p.POST("/imports/procurement", manager, a.importProcurement)
	p.GET("/imports/template", a.importTemplate)

	p.GET("/invoices", a.listInvoices)
	p.GET("/invoices/:id", a.getInvoice)
	p.POST("/invoices/:id/verify", a.verify)
	p.GET("/invoices/:id/checks", a.invoiceChecks)
	p.GET("/invoices/:id/exceptions", a.invoiceExceptions)
	p.GET("/invoices/:id/events", a.invoiceEvents)
	p.GET("/invoices/:id/comparison", a.comparison)
	p.POST("/invoices/:id/po-match", a.poMatch)
	p.POST("/invoices/:id/approve", manager, a.approve)
	p.POST("/invoices/:id/reject", manager, a.reject)
	p.POST("/invoices/:id/override", manager, a.override)
	p.POST("/invoices/:id/return", manager, a.returnToClerk)
	p.POST("/invoices/:id/resubmit", a.resubmit)
	p.POST("/invoices/:id/submit", a.submitForApproval)

	p.GET("/exceptions", a.listExceptions)
	p.POST("/exceptions/:id", a.updateException)

	p.GET("/tolerance-rules", a.listTolerances)
	p.POST("/tolerance-rules", admin, a.createTolerance)
	return r
}

func (a *API) logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/healthz" {
			return
		}
		a.Log.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "ms", time.Since(start).Milliseconds())
	}
}

func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		o := c.GetHeader("Origin")
		if allowed[o] || allowed["*"] {
			c.Header("Access-Control-Allow-Origin", o)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// fail maps service errors to HTTP responses without leaking internals.
func (a *API) fail(c *gin.Context, err error) {
	var ue *service.UserError
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case errors.As(err, &ue):
		msg = ue.Msg
		switch {
		case errors.Is(err, service.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, service.ErrForbidden):
			status = http.StatusForbidden
		case errors.Is(err, service.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, service.ErrUnsupported):
			status = http.StatusUnsupportedMediaType
		case errors.Is(err, service.ErrTooLarge):
			status = http.StatusRequestEntityTooLarge
		default:
			status = http.StatusBadRequest
		}
	case errors.Is(err, service.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		status, msg = http.StatusNotFound, "not found"
	default:
		a.Log.Error("request failed", "path", c.Request.URL.Path, "err", err)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func bad(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": msg})
}

// rawJSON runs a query returning a single json value and writes it.
func (a *API) rawJSON(c *gin.Context, q string, args ...any) {
	var b []byte
	if err := a.DB.QueryRowContext(c, q, args...).Scan(&b); err != nil {
		a.fail(c, err)
		return
	}
	if b == nil || string(b) == "null" {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Data(http.StatusOK, "application/json", b)
}

// list wraps a SELECT in json_agg with paging; `inner` must select the row columns.
func (a *API) list(c *gin.Context, inner string, args ...any) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	n := len(args)
	q := `SELECT json_build_object('items', COALESCE((SELECT json_agg(t) FROM (` + inner + ` LIMIT $` + strconv.Itoa(n+1) + ` OFFSET $` + strconv.Itoa(n+2) +
		`) t), '[]'::json), 'total', (SELECT count(*) FROM (` + inner + `) c), 'limit', $` + strconv.Itoa(n+1) + `::int, 'offset', $` + strconv.Itoa(n+2) + `::int)`
	a.rawJSON(c, q, append(args, limit, offset)...)
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// idParam validates the :id path parameter.
func idParam(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !validUUID(id) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return "", false
	}
	return id, true
}

func ok(c *gin.Context, v any) { c.JSON(http.StatusOK, v) }
