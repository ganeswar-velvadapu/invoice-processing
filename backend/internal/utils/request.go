package utils

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ValidUUID reports whether s looks like a UUID (all our ids are UUIDs).
func ValidUUID(s string) bool {
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

// IDParam returns the :id from the URL, answering 404 itself when it isn't a valid id.
func IDParam(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !ValidUUID(id) {
		NotFound(c)
		return "", false
	}
	return id, true
}
