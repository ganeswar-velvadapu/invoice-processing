// Package auth implements password hashing, JWT issuance and role-based Gin middleware.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleClerk   = "AP_CLERK"
	RoleManager = "AP_MANAGER"
	RoleAdmin   = "ADMIN"
)

var ValidRoles = map[string]bool{RoleClerk: true, RoleManager: true, RoleAdmin: true}

// rank lets "at least manager" style checks work; ADMIN includes manager rights.
var rank = map[string]int{RoleClerk: 1, RoleManager: 2, RoleAdmin: 3}

type User struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

type Claims struct {
	Role  string `json:"role"`
	Email string `json:"email"`
	jwt.RegisteredClaims
}

type Service struct {
	DB     *sql.DB
	Secret []byte
	TTL    time.Duration
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

var ErrInvalidCredentials = errors.New("invalid email or password")

func (s *Service) Login(ctx context.Context, email, password string) (string, *User, error) {
	var u User
	var hash string
	err := s.DB.QueryRowContext(ctx,
		`SELECT user_id, email, full_name, role, is_active, password_hash FROM users WHERE lower(email)=lower($1)`,
		strings.TrimSpace(email)).Scan(&u.UserID, &u.Email, &u.FullName, &u.Role, &u.IsActive, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if !u.IsActive || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", nil, ErrInvalidCredentials
	}
	tok, err := s.Issue(&u)
	return tok, &u, err
}

func (s *Service) Issue(u *User) (string, error) {
	now := time.Now()
	c := Claims{Role: u.Role, Email: u.Email, RegisteredClaims: jwt.RegisteredClaims{
		Subject: u.UserID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(s.TTL)),
		Issuer: "invoiceproc",
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.Secret)
}

func (s *Service) Parse(tok string) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(tok, c, func(t *jwt.Token) (any, error) { return s.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("invoiceproc"))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// EnsureAdmin creates the bootstrap admin if there are no users yet.
func (s *Service) EnsureAdmin(ctx context.Context, email, password string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if len(password) < 8 {
		return false, errors.New("no users exist: set ADMIN_PASSWORD (min 8 chars) to bootstrap the admin")
	}
	h, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO users (email, full_name, password_hash, role) VALUES ($1,'Administrator',$2,'ADMIN')`, email, h)
	return err == nil, err
}

const ctxKey = "auth.claims"

// Middleware authenticates a Bearer token and checks the user is still active.
func (s *Service) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tok := strings.TrimPrefix(h, "Bearer ")
		if tok == "" || tok == h {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		claims, err := s.Parse(tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		var active bool
		var role string
		if err := s.DB.QueryRowContext(c, `SELECT is_active, role FROM users WHERE user_id=$1`, claims.Subject).Scan(&active, &role); err != nil || !active {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user inactive"})
			return
		}
		claims.Role = role // role changes take effect immediately
		c.Set(ctxKey, claims)
		c.Next()
	}
}

// Require allows the request only if the user's role is at least `min`.
func Require(min string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rank[CurrentRole(c)] < rank[min] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions: requires " + min})
			return
		}
		c.Next()
	}
}

func HasRole(c *gin.Context, min string) bool { return rank[CurrentRole(c)] >= rank[min] }

func CurrentClaims(c *gin.Context) *Claims {
	v, ok := c.Get(ctxKey)
	if !ok {
		return nil
	}
	return v.(*Claims)
}

func CurrentUserID(c *gin.Context) string {
	if cl := CurrentClaims(c); cl != nil {
		return cl.Subject
	}
	return ""
}

func CurrentRole(c *gin.Context) string {
	if cl := CurrentClaims(c); cl != nil {
		return cl.Role
	}
	return ""
}
