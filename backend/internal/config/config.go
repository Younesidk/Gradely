// Package config reads runtime configuration from environment variables.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

	// Developer integrations (MCP endpoint). The credential limit applies to each API key and each OAuth
	// connection; the user limit applies across all of a user's credentials together.
	CredentialRateLimit int
	UserRateLimit       int
	RateLimitWindow     time.Duration
	ActivityRetention   time.Duration
}

// MCPResource is the canonical URL of the MCP endpoint, which OAuth access tokens are bound to (RFC 8707).
func (c Config) MCPResource() string { return c.BaseURL + "/mcp" }

// Validate rejects an APP_BASE_URL that cannot be the app's public origin. Invitation and email-change links,
// the OAuth issuer and the MCP resource are all built from it, so a typo would quietly break every one of them.
func (c Config) Validate() error {
	return validateBaseURL(c.BaseURL)
}

func validateBaseURL(raw string) error {
	if strings.ContainsAny(raw, " \t\"'") {
		return fmt.Errorf("APP_BASE_URL %q contains spaces or quotes; set only the URL, e.g. https://gradly.example.com", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("APP_BASE_URL %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("APP_BASE_URL %q must start with https:// (or http:// for local use)", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("APP_BASE_URL %q must not contain credentials, a query or a '#' (is a comment on the same line?)", raw)
	}
	if u.Path != "" {
		return fmt.Errorf("APP_BASE_URL %q must be an origin without a path, e.g. https://gradly.example.com", raw)
	}
	if !validHost(u.Hostname()) {
		return fmt.Errorf("APP_BASE_URL %q has an invalid host name", raw)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("APP_BASE_URL %q has an invalid port", raw)
		}
	}
	return nil
}

// validHost accepts IP addresses and DNS names made of letters, digits and inner hyphens.
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
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
		CredentialRateLimit: positiveInt("GRADLY_API_KEY_RATE_LIMIT", 100),
		UserRateLimit:       positiveInt("GRADLY_USER_RATE_LIMIT", 150),
		RateLimitWindow:     positiveDuration("GRADLY_RATE_LIMIT_WINDOW", 5*time.Minute),
		ActivityRetention:   positiveDuration("GRADLY_ACTIVITY_RETENTION", 90*24*time.Hour),
	}
}

func positiveInt(key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// positiveDuration accepts Go durations ("5m", "90s") and plain days ("90d").
func positiveDuration(key string, fallback time.Duration) time.Duration {
	s := strings.TrimSpace(os.Getenv(key))
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour
		}
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
