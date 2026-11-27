// Package utils holds small HTTP helpers shared by the API handlers.
package utils

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/service"
)

// OK sends 200 with v as JSON.
func OK(c *gin.Context, v any) { c.JSON(http.StatusOK, v) }

// Bad sends 400 Bad Request with a message.
func Bad(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": msg})
}

// NotFound sends 404.
func NotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
}

// Fail turns an error into the right HTTP status. Messages meant for users (service.UserError) are
// shown; anything unexpected becomes 500 "internal error" and the real error only goes to the log.
func Fail(c *gin.Context, err error) {
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
		slog.Error("request failed", "path", c.Request.URL.Path, "err", err)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
