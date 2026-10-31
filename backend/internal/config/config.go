// Package config reads runtime configuration from environment variables.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr                string
	DataDir             string
	BaseURL             string
	ResendAPIKey        string
	ResendFromEmail     string
	AuthRequestsPerMin  int
	InvitationValidDays int
	SecureCookies       bool

	AdminEmail        string
	AdminUsername     string
	AdminPassword     string
	DefaultLabFormula string
}

func (c Config) EmailConfigured() bool {
	return strings.TrimSpace(c.ResendAPIKey) != "" && strings.TrimSpace(c.ResendFromEmail) != ""
}

func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "gradly-go.db") }

// FromEnv builds the configuration. Every key can be set as an environment variable.
func FromEnv() Config {
	dir := os.Getenv("GRADLY_DATA_DIR")
	if strings.TrimSpace(dir) == "" {
		dir = "data"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	base := strings.TrimRight(env("APP_BASE_URL", "http://localhost:8080"), "/")
	limit, err := strconv.Atoi(os.Getenv("GRADLY_AUTH_RATE_LIMIT"))
	if err != nil || limit <= 0 {
		limit = 10
	}
	return Config{
		Addr:                env("GRADLY_ADDR", ":"+env("PORT", "8080")),
		DataDir:             abs,
		BaseURL:             base,
		ResendAPIKey:        os.Getenv("RESEND_API_KEY"),
		ResendFromEmail:     os.Getenv("RESEND_FROM_EMAIL"),
		AuthRequestsPerMin:  limit,
		InvitationValidDays: 7,
		SecureCookies:       strings.EqualFold(os.Getenv("GRADLY_SECURE_COOKIES"), "true"),
		AdminEmail:          os.Getenv("ADMIN_EMAIL"),
		AdminUsername:       os.Getenv("ADMIN_USERNAME"),
		AdminPassword:       os.Getenv("ADMIN_PASSWORD"),
		DefaultLabFormula:   os.Getenv("DEFAULT_LAB_FORMULA"),
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
