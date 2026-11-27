package utils

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS lets browsers on the listed origins (CORS_ORIGINS) call the API, and answers the browser's
// "may I?" OPTIONS request.
func CORS(origins []string) gin.HandlerFunc {
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
