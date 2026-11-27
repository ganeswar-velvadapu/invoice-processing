// Package handlers holds the HTTP handlers, one file per area. Each handler reads the request,
// calls the database or a service function, and writes the response. Routes are in internal/api.
package handlers

import (
	"database/sql"

	"invoiceproc/internal/service"
)

// Handler is shared by every handler: the app's functions and the database.
type Handler struct {
	Svc *service.Service
	DB  *sql.DB
}
