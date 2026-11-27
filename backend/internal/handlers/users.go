package handlers

// User accounts and roles (admin).

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"invoiceproc/internal/auth"
	"invoiceproc/internal/utils"
)

func (h *Handler) ListUsers(c *gin.Context) {
	utils.SendPage(c, h.DB, `SELECT user_id, email, full_name, role, is_active, created_at FROM users ORDER BY email`)
}

func (h *Handler) CreateUser(c *gin.Context) {
	var b struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if c.ShouldBindJSON(&b) != nil || !strings.Contains(b.Email, "@") || len(b.Password) < 8 || !auth.ValidRoles[b.Role] {
		utils.Bad(c, "email, password (min 8 chars) and a valid role (AP_CLERK, AP_MANAGER, ADMIN) are required")
		return
	}
	hash, err := auth.HashPassword(b.Password)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	var id string
	if err := h.DB.QueryRowContext(c, `INSERT INTO users (email, full_name, password_hash, role) VALUES (lower($1),$2,$3,$4)
		ON CONFLICT (email) DO NOTHING RETURNING user_id`, strings.TrimSpace(b.Email), b.FullName, hash, b.Role).Scan(&id); err != nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "a user with that email already exists"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user_id": id})
}

func (h *Handler) UpdateUser(c *gin.Context) {
	id, okk := utils.IDParam(c)
	if !okk {
		return
	}
	var b struct {
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
		Password *string `json:"password"`
		FullName *string `json:"full_name"`
	}
	if c.ShouldBindJSON(&b) != nil {
		utils.Bad(c, "invalid body")
		return
	}
	if b.Role != nil && !auth.ValidRoles[*b.Role] {
		utils.Bad(c, "invalid role")
		return
	}
	if id == auth.CurrentUserID(c) && ((b.IsActive != nil && !*b.IsActive) || (b.Role != nil && *b.Role != auth.RoleAdmin)) {
		utils.Bad(c, "you cannot deactivate or demote yourself")
		return
	}
	var hash *string
	if b.Password != nil {
		if len(*b.Password) < 8 {
			utils.Bad(c, "password must be at least 8 characters")
			return
		}
		h, _ := auth.HashPassword(*b.Password)
		hash = &h
	}
	r, err := h.DB.ExecContext(c, `UPDATE users SET role=COALESCE($2,role), is_active=COALESCE($3,is_active), password_hash=COALESCE($4,password_hash),
		full_name=COALESCE($5,full_name), updated_at=now() WHERE user_id=$1`, id, b.Role, b.IsActive, hash, b.FullName)
	if err != nil {
		utils.Fail(c, err)
		return
	}
	if n, _ := r.RowsAffected(); n == 0 {
		utils.Fail(c, utils.ErrNotFound)
		return
	}
	utils.OK(c, gin.H{"updated": true})
}
