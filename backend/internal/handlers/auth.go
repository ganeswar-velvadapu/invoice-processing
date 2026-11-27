package handlers

// Login and the current user.

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/utils"
)

func (h *Handler) Login(c *gin.Context) {
	var body struct{ Email, Password string }
	if c.ShouldBindJSON(&body) != nil {
		utils.Bad(c, "email and password required")
		return
	}
	cfg := h.Svc.Cfg
	tok, u, err := auth.Login(c, h.DB, []byte(cfg.JWTSecret), cfg.JWTTTL, body.Email, body.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		utils.Fail(c, err)
		return
	}
	utils.OK(c, gin.H{"token": tok, "user": u})
}

func (h *Handler) Me(c *gin.Context) {
	utils.SendRow(c, h.DB, `SELECT user_id, email, full_name, role, is_active FROM users WHERE user_id=$1`, auth.CurrentUserID(c))
}
