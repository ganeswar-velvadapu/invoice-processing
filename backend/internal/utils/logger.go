package utils

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger writes one line per request after it is handled (skipping /healthz, which Docker calls often).
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/healthz" {
			return
		}
		slog.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds())
	}
}
