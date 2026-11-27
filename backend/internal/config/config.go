// Package config loads runtime configuration from the environment (and an optional .env file).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL    string
	ListenAddr     string
	StorageDir     string
	JWTSecret      string
	JWTTTL         time.Duration
	MaxUploadBytes int64
	AdminEmail     string
	AdminPassword  string
	CORSOrigins    []string
	BuyerGSTINs    []string // our own company GSTINs, helps tell seller from buyer
	PdftotextBin   string
	PdftoppmBin    string
	GSTINChecksum  string
	OllamaURL      string
	VLMModel       string
	VLMPageTimeout time.Duration
	VLMMaxPx       int
	// Email ingestion (IMAP). Enabled when host, user and password are all set.
	IMAPHost     string
	IMAPPort     int
	IMAPUser     string
	IMAPPassword string
	IMAPFolder   string
	IMAPTLS      bool
	IMAPPoll     time.Duration
}

// IMAPEnabled reports whether email ingestion is configured.
func (c *Config) IMAPEnabled() bool {
	return c.IMAPHost != "" && c.IMAPUser != "" && c.IMAPPassword != ""
}

// LoadDotEnv sets variables from a .env file without overriding the real environment.
func LoadDotEnv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if _, exists := os.LookupEnv(k); !exists {
				os.Setenv(k, v)
			}
		}
		f.Close()
	}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func list(k string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(k), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func Load() (*Config, error) {
	LoadDotEnv(".env", "../.env")
	var err error
	c := &Config{
		DatabaseURL:   env("DATABASE_URL", ""),
		ListenAddr:    env("LISTEN_ADDR", ":8080"),
		StorageDir:    env("STORAGE_DIR", "../data/storage"),
		JWTSecret:     env("JWT_SECRET", ""),
		AdminEmail:    env("ADMIN_EMAIL", "admin@example.com"),
		AdminPassword: env("ADMIN_PASSWORD", ""),
		CORSOrigins:   list("CORS_ORIGINS"),
		BuyerGSTINs:   list("BUYER_GSTINS"),
		PdftotextBin:  env("PDFTOTEXT_BIN", "pdftotext"),
		PdftoppmBin:   env("PDFTOPPM_BIN", "pdftoppm"),
		GSTINChecksum: env("GSTIN_CHECKSUM", "strict"),
		OllamaURL:     env("OLLAMA_URL", "http://ollama:11434"),
		VLMModel:      env("VLM_MODEL", "qwen3-vl:4b-instruct"),
		IMAPHost:      env("IMAP_HOST", ""),
		IMAPUser:      env("IMAP_USER", ""),
		IMAPPassword:  strings.ReplaceAll(env("IMAP_PASSWORD", ""), " ", ""), // Google shows app passwords in groups of four
		IMAPFolder:    env("IMAP_FOLDER", "INBOX"),
		IMAPTLS:       env("IMAP_TLS", "true") != "false",
	}
	if c.IMAPPort, err = strconv.Atoi(env("IMAP_PORT", "993")); err != nil {
		return nil, fmt.Errorf("IMAP_PORT: %w", err)
	}
	if c.IMAPPoll, err = time.ParseDuration(env("IMAP_POLL_INTERVAL", "60s")); err != nil || c.IMAPPoll < 10*time.Second {
		return nil, fmt.Errorf("IMAP_POLL_INTERVAL must be a duration of at least 10s")
	}
	if len(c.CORSOrigins) == 0 {
		c.CORSOrigins = []string{"http://localhost:3000"}
	}
	ttl, err := time.ParseDuration(env("JWT_TTL", "12h"))
	if err != nil {
		return nil, fmt.Errorf("JWT_TTL: %w", err)
	}
	c.JWTTTL = ttl
	mb, err := strconv.ParseInt(env("MAX_UPLOAD_MB", "50"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("MAX_UPLOAD_MB: %w", err)
	}
	c.MaxUploadBytes = mb << 20
	if c.VLMPageTimeout, err = time.ParseDuration(env("VLM_PAGE_TIMEOUT", "10m")); err != nil {
		return nil, fmt.Errorf("VLM_PAGE_TIMEOUT: %w", err)
	}
	c.VLMMaxPx, _ = strconv.Atoi(env("VLM_MAX_PX", "1600"))
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	return c, nil
}
