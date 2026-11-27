// Package auth is login: password hashing, login tokens (JWT) and role checks. Plain functions;
// the database and the JWT secret are passed in.
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

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

var ErrInvalidCredentials = errors.New("invalid email or password")

// Login checks email + password and returns a signed token valid for ttl.
func Login(ctx context.Context, database *sql.DB, secret []byte, ttl time.Duration, email, password string) (string, *User, error) {
	var u User
	var hash string
	err := database.QueryRowContext(ctx,
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
	tok, err := issueToken(&u, secret, ttl)
	return tok, &u, err
}

// issueToken creates the login token: who the user is, their role, and when it expires, signed with secret.
func issueToken(u *User, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()
	c := Claims{Role: u.Role, Email: u.Email, RegisteredClaims: jwt.RegisteredClaims{
		Subject: u.UserID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		Issuer: "invoiceproc",
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

// parseToken checks a token's signature and expiry and returns what it says.
func parseToken(tok string, secret []byte) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(tok, c, func(t *jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("invoiceproc"))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// EnsureAdmin creates the first admin (from .env) when there are no users yet.
func EnsureAdmin(ctx context.Context, database *sql.DB, email, password string) (bool, error) {
	var n int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
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
	_, err = database.ExecContext(ctx, `INSERT INTO users (email, full_name, password_hash, role) VALUES ($1,'Administrator',$2,'ADMIN')`, email, h)
	return err == nil, err
}

const ctxKey = "auth.claims"

// RequireLogin runs before protected routes: it checks the Bearer token and that the user is still active.
func RequireLogin(database *sql.DB, secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tok := strings.TrimPrefix(h, "Bearer ")
		if tok == "" || tok == h {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		claims, err := parseToken(tok, secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		var active bool
		var role string
		if err := database.QueryRowContext(c, `SELECT is_active, role FROM users WHERE user_id=$1`, claims.Subject).Scan(&active, &role); err != nil || !active {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user inactive"})
			return
		}
		claims.Role = role // role changes take effect immediately
		c.Set(ctxKey, claims)
		c.Next()
	}
}

// RequireRole lets the request through only if the user's role is one of roles.
// Use it on a route after RequireLogin, e.g. p.POST("/users", auth.RequireRole(auth.RoleAdmin), h.CreateUser).
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !HasRole(c, roles...) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "your role is not allowed to do this"})
			return
		}
		c.Next()
	}
}

// HasRole reports whether the logged-in user's role is one of roles (for checks inside a handler).
func HasRole(c *gin.Context, roles ...string) bool {
	role := CurrentRole(c)
	for _, r := range roles {
		if role == r {
			return true
		}
	}
	return false
}

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
