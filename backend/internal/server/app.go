// Package server wires the HTTP API (Gin) to the services that use GORM.
package server

import (
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/younesidk/gradely/internal/config"
	"github.com/younesidk/gradely/internal/mail"
	"github.com/younesidk/gradely/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// App holds everything the handlers share.
type App struct {
	DB         *gorm.DB
	Cfg        config.Config
	Mail       mail.Sender
	Now        func() time.Time
	BcryptCost int

	limiter *rateLimiter
	storage *storageCache
	dummy   string // hash compared against when the account doesn't exist, to keep timing similar

	mcpLimiter   *slidingLimiter // per credential and per user, on the MCP endpoint
	authFailures *slidingLimiter // failed bearer tokens per client address
	mcp          *mcpServers
	eventCount   atomic.Uint64
}

// OpenDB opens (and migrates) the SQLite database at path.
func OpenDB(path string) (*gorm.DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{SlowThreshold: time.Second, LogLevel: logger.Warn, IgnoreRecordNotFoundError: true}),
		DisableForeignKeyConstraintWhenMigrating: false,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1) // one writer at a time; keeps transactions simple and avoids SQLITE_BUSY
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, err
	}
	return db, nil
}

// New builds the application.
func New(cfg config.Config, db *gorm.DB, sender mail.Sender) *App {
	if cfg.CredentialRateLimit <= 0 {
		cfg.CredentialRateLimit = 100
	}
	if cfg.UserRateLimit <= 0 {
		cfg.UserRateLimit = 150
	}
	if cfg.RateLimitWindow <= 0 {
		cfg.RateLimitWindow = 5 * time.Minute
	}
	if cfg.ActivityRetention <= 0 {
		cfg.ActivityRetention = 90 * 24 * time.Hour
	}
	a := &App{DB: db, Cfg: cfg, Mail: sender, Now: func() time.Time { return time.Now().UTC() }, BcryptCost: 12}
	a.limiter = newRateLimiter(cfg.AuthRequestsPerMin, time.Minute)
	a.storage = &storageCache{}
	a.mcpLimiter = newSlidingLimiter(cfg.RateLimitWindow)
	a.authFailures = newSlidingLimiter(time.Minute)
	return a
}

func (a *App) now() time.Time { return a.Now().UTC() }

func (a *App) hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), a.BcryptCost)
	return string(b), err
}

// ---------- errors ----------

// userError is a problem the user can fix. Its message is safe to show.
type userError struct {
	msg    string
	status int
}

func (e *userError) Error() string { return e.msg }

func badRequest(msg string) error { return &userError{msg, http.StatusBadRequest} }
func notFound(what string) error {
	if what == "" {
		what = "That item"
	}
	return &userError{what + " was not found.", http.StatusNotFound}
}

// fail writes the error as JSON. Unexpected errors are logged and hidden.
func fail(c *gin.Context, err error) {
	var ue *userError
	if errors.As(err, &ue) {
		c.AbortWithStatusJSON(ue.status, gin.H{"error": ue.msg})
		return
	}
	slog.Error("request failed", "path", c.Request.URL.Path, "error", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred. Please try again."})
}

// ---------- router ----------

// Router builds the Gin engine: the JSON API under /api and the Svelte app for everything else.
func (a *App) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), securityHeaders())
	r.TrustedPlatform = gin.PlatformCloudflare
	_ = r.SetTrustedProxies(nil)

	api := r.Group("/api")
	api.Use(a.requireJSONHeader())

	auth := api.Group("/auth")
	auth.Use(a.rateLimit())
	auth.POST("/login", a.login)
	auth.GET("/invitation", a.invitationInfo)
	auth.POST("/register", a.register)
	auth.POST("/confirm-email-change", a.confirmEmailChange)
	api.POST("/auth/logout", a.logout)
	api.GET("/auth/me", a.loadSession(false), a.me)

	user := api.Group("")
	user.Use(a.loadSession(true))
	user.GET("/account", a.getAccount)
	user.PUT("/account/username", a.changeUserName)
	user.POST("/account/email-change", a.requestEmailChange)
	user.PUT("/account/password", a.changePassword)

	user.GET("/dashboard", a.dashboard)
	user.GET("/semesters", a.listSemesters)
	user.POST("/semesters", a.createSemester)
	user.GET("/semesters/:id", a.getSemester)
	user.PUT("/semesters/:id", a.updateSemester)
	user.DELETE("/semesters/:id", a.deleteSemester)
	user.POST("/semesters/:id/move", a.moveSemester)
	user.POST("/semesters/:id/modules", a.addModule)
	user.PUT("/modules/:id", a.updateModule)
	user.PUT("/modules/:id/grades", a.updateGrades)
	user.PUT("/modules/:id/formula", a.setModuleFormula)
	user.DELETE("/modules/:id", a.deleteModule)
	user.POST("/modules/:id/move", a.moveModule)

	user.GET("/formulas", a.listFormulas)
	user.POST("/formulas", a.createFormula)
	user.POST("/formulas/preview", a.previewFormula)
	user.POST("/formulas/platform-lab", a.addPlatformLabFormula)
	user.PUT("/formulas/:id", a.updateFormula)
	user.POST("/formulas/:id/default", a.setDefaultFormula)
	user.DELETE("/formulas/:id", a.deleteFormula)

	admin := api.Group("/admin")
	admin.Use(a.loadSession(true), a.requireAdmin())
	admin.GET("/overview", a.adminOverview)
	admin.PUT("/default-lab-formula", a.adminSetDefaultLabFormula)
	admin.GET("/users", a.adminUsers)
	admin.GET("/users/:id", a.adminUser)
	admin.GET("/users/:id/semesters/:sid", a.adminUserSemester)
	admin.POST("/users/:id/disabled", a.adminSetDisabled)
	admin.GET("/invitations", a.listInvitations)
	admin.POST("/invitations", a.createInvitation)
	admin.POST("/invitations/:id/regenerate", a.regenerateInvitation)
	admin.POST("/invitations/:id/revoke", a.revokeInvitation)

	// Developer integrations: managed from the signed-in web app (session + X-Gradly header) ...
	integ := user.Group("/integrations")
	integ.GET("/meta", a.integrationsMeta)
	integ.GET("/overview", a.integrationsOverview)
	integ.GET("/keys", a.listAPIKeys)
	integ.POST("/keys", a.createAPIKey)
	integ.PUT("/keys/:id", a.updateAPIKey)
	integ.POST("/keys/:id/rotate", a.rotateAPIKey)
	integ.POST("/keys/:id/revoke", a.revokeAPIKey)
	integ.DELETE("/keys/:id", a.deleteAPIKey)
	integ.GET("/apps", a.listApps)
	integ.POST("/apps/:id/revoke", a.revokeApp)
	integ.DELETE("/apps/:id", a.deleteApp)
	integ.GET("/activity", a.listActivity)
	user.GET("/oauth/authorize", a.authorizeInfo)
	user.POST("/oauth/authorize", a.authorizeDecision)

	// ... and used by MCP clients: OAuth endpoints and the MCP endpoint itself (bearer tokens, no cookies).
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		r.GET(p, a.protectedResourceMetadata)
		r.OPTIONS(p, a.oauthPreflight)
	}
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp"} {
		r.GET(p, a.authorizationServerMetadata)
		r.OPTIONS(p, a.oauthPreflight)
	}
	oauth := r.Group("/oauth")
	oauth.POST("/register", a.registerClient)
	oauth.POST("/token", a.token)
	oauth.POST("/revoke", a.revokeToken)
	for _, p := range []string{"/register", "/token", "/revoke"} {
		oauth.OPTIONS(p, a.oauthPreflight)
	}
	mcpEntry := a.mcpEntry(a.mcpHandler())
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions} {
		r.Handle(m, "/mcp", mcpEntry)
	}

	r.NoRoute(staticHandler())
	return r
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		c.Next()
	}
}

// requireJSONHeader is the CSRF defence for the JSON API: state-changing requests must carry a custom header,
// which browsers never attach to cross-site requests without a CORS preflight (and this API sends no CORS headers).
func (a *App) requireJSONHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if c.GetHeader("X-Gradly") != "1" {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request."})
				return
			}
		}
		c.Next()
	}
}

// ---------- rate limiting ----------

type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	start time.Time
	count int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, hits: map[string]*bucket{}}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10000 { // bound memory
		for k, b := range l.hits {
			if now.Sub(b.start) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	b, ok := l.hits[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.hits[key] = &bucket{start: now, count: 1}
		return true
	}
	b.count++
	return b.count <= l.limit
}

// rateLimit limits the authentication-sensitive endpoints per client address.
func (a *App) rateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.limiter.allow(c.ClientIP(), time.Now()) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many attempts. Please wait a minute and try again."})
			return
		}
		c.Next()
	}
}

// ---------- small helpers ----------

func clean(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func bind(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		fail(c, badRequest("That request couldn't be read."))
		return false
	}
	return true
}
