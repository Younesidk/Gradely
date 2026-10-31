package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

const (
	maxEventsPerUser    = 5000
	activityPruneEvery  = 200 // inserts between retention passes
	defaultActivityPage = 25
)

// ---------- activity log ----------

// logEvent appends one line to the user's integration activity. Only the operation name and outcome are kept:
// never arguments, academic values, tokens or headers.
func (a *App) logEvent(p *principal, operation, category string, success bool, status int, errCode string, duration time.Duration) {
	ev := models.IntegrationEvent{
		UserID: p.User.ID, CreatedAt: a.now(), CredentialType: p.Kind, CredentialID: p.CredentialID,
		CredentialName: truncate(p.Name, 100), Operation: operation, Category: category, Success: success,
		Status: status, DurationMs: duration.Milliseconds(),
	}
	if errCode != "" {
		ev.ErrorCode = &errCode
	}
	if err := a.DB.Create(&ev).Error; err != nil {
		slog.Error("could not record integration activity", "error", err)
		return
	}
	if a.eventCount.Add(1)%activityPruneEvery == 0 {
		a.pruneEvents(p.User.ID)
	}
}

// pruneEvents applies the retention policy: events older than the retention period go, and each user keeps at most
// maxEventsPerUser of the newest.
func (a *App) pruneEvents(userID string) {
	a.DB.Where("created_at < ?", a.now().Add(-a.Cfg.ActivityRetention)).Delete(&models.IntegrationEvent{})
	var cutoff uint64
	a.DB.Model(&models.IntegrationEvent{}).Where("user_id = ?", userID).Order("id DESC").
		Offset(maxEventsPerUser).Limit(1).Pluck("id", &cutoff)
	if cutoff > 0 {
		a.DB.Where("user_id = ? AND id <= ?", userID, cutoff).Delete(&models.IntegrationEvent{})
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

type eventJSON struct {
	ID             uint64    `json:"id"`
	Time           time.Time `json:"time"`
	CredentialType string    `json:"credentialType"`
	CredentialID   string    `json:"credentialId"`
	CredentialName string    `json:"credentialName"`
	Operation      string    `json:"operation"`
	Category       string    `json:"category"`
	Success        bool      `json:"success"`
	Status         int       `json:"status"`
	ErrorCode      *string   `json:"errorCode"`
	DurationMs     int64     `json:"durationMs"`
}

func toEventJSON(e models.IntegrationEvent) eventJSON {
	return eventJSON{e.ID, e.CreatedAt, e.CredentialType, e.CredentialID, e.CredentialName, e.Operation, e.Category,
		e.Success, e.Status, e.ErrorCode, e.DurationMs}
}

func (a *App) listActivity(c *gin.Context) {
	user := current(c)
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.Query("pageSize"))
	if size < 1 || size > 100 {
		size = defaultActivityPage
	}
	q := a.DB.Model(&models.IntegrationEvent{}).Where("user_id = ?", user.ID)
	if cred := c.Query("credential"); cred != "" {
		q = q.Where("credential_id = ?", cred)
	}
	if op := c.Query("operation"); op != "" {
		q = q.Where("operation = ?", op)
	}
	switch c.Query("status") {
	case "success":
		q = q.Where("success = ?", true)
	case "failure":
		q = q.Where("success = ?", false)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, err)
		return
	}
	var rows []models.IntegrationEvent
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	items := make([]eventJSON, 0, len(rows))
	for _, r := range rows {
		items = append(items, toEventJSON(r))
	}

	// Filter choices: every integration and operation that appears in this user's log.
	type credRow struct {
		CredentialID   string `json:"id"`
		CredentialType string `json:"type"`
		CredentialName string `json:"name"`
	}
	var creds []credRow
	a.DB.Model(&models.IntegrationEvent{}).Where("user_id = ?", user.ID).
		Select("credential_id, credential_type, MAX(credential_name) AS credential_name").
		Group("credential_id, credential_type").Order("credential_name").Scan(&creds)
	var ops []string
	a.DB.Model(&models.IntegrationEvent{}).Where("user_id = ?", user.ID).Distinct("operation").Order("operation").Pluck("operation", &ops)
	if creds == nil {
		creds = []credRow{}
	}
	if ops == nil {
		ops = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": size, "total": total,
		"credentials": creds, "operations": ops, "retentionDays": int(a.Cfg.ActivityRetention.Hours() / 24)})
}

// ---------- overview & metadata ----------

func (a *App) integrationLimits() gin.H {
	return gin.H{"perCredential": a.Cfg.CredentialRateLimit, "perUser": a.Cfg.UserRateLimit,
		"windowSeconds": int(a.Cfg.RateLimitWindow.Seconds())}
}

func (a *App) integrationsMeta(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"mcpUrl": a.Cfg.MCPResource(), "issuer": a.Cfg.BaseURL, "scopes": scopeCatalog, "readScopes": readScopes(),
		"limits": a.integrationLimits(), "retentionDays": int(a.Cfg.ActivityRetention.Hours() / 24),
	})
}

func (a *App) integrationsOverview(c *gin.Context) {
	user := current(c)
	now := a.now()
	var activeKeys, revokedKeys, connected, revokedApps int64
	a.DB.Model(&models.APIKey{}).Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", user.ID, now).Count(&activeKeys)
	a.DB.Model(&models.APIKey{}).Where("user_id = ? AND revoked_at IS NOT NULL", user.ID).Count(&revokedKeys)
	a.DB.Model(&models.OAuthGrant{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Count(&connected)
	a.DB.Model(&models.OAuthGrant{}).Where("user_id = ? AND revoked_at IS NOT NULL", user.ID).Count(&revokedApps)

	var keyRequests, appRequests int64
	a.DB.Model(&models.APIKey{}).Where("user_id = ?", user.ID).Select("COALESCE(SUM(request_count), 0)").Scan(&keyRequests)
	a.DB.Model(&models.OAuthGrant{}).Where("user_id = ?", user.ID).Select("COALESCE(SUM(request_count), 0)").Scan(&appRequests)

	var recent []models.IntegrationEvent
	a.DB.Where("user_id = ?", user.ID).Order("id DESC").Limit(6).Find(&recent)
	items := make([]eventJSON, 0, len(recent))
	for _, r := range recent {
		items = append(items, toEventJSON(r))
	}
	var failures int64
	a.DB.Model(&models.IntegrationEvent{}).Where("user_id = ? AND success = ? AND created_at > ?", user.ID, false, now.Add(-24*time.Hour)).Count(&failures)

	c.JSON(http.StatusOK, gin.H{
		"activeKeys": activeKeys, "connectedApps": connected, "revokedCredentials": revokedKeys + revokedApps,
		"totalRequests": keyRequests + appRequests, "windowRequests": a.mcpLimiter.count("user:"+user.ID, now),
		"failuresLast24h": failures, "limits": a.integrationLimits(), "recent": items, "mcpUrl": a.Cfg.MCPResource(),
	})
}

// ---------- connected OAuth applications ----------

type appJSON struct {
	ID           string     `json:"id"`
	ClientID     string     `json:"clientId"`
	ClientName   string     `json:"clientName"`
	RedirectHost string     `json:"redirectHost"`
	Scopes       []string   `json:"scopes"`
	Status       string     `json:"status"` // connected, revoked
	RequestCount int64      `json:"requestCount"`
	AuthorizedAt time.Time  `json:"authorizedAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt"`
	RevokedAt    *time.Time `json:"revokedAt"`
}

func (a *App) listApps(c *gin.Context) {
	var grants []models.OAuthGrant
	if err := a.DB.Where("user_id = ?", current(c).ID).Order("revoked_at IS NOT NULL, updated_at DESC").Find(&grants).Error; err != nil {
		fail(c, err)
		return
	}
	ids := make([]string, 0, len(grants))
	for _, g := range grants {
		ids = append(ids, g.ClientID)
	}
	var clients []models.OAuthClient
	if len(ids) > 0 {
		a.DB.Where("id IN ?", ids).Find(&clients)
	}
	byID := map[string]models.OAuthClient{}
	for _, cl := range clients {
		byID[cl.ID] = cl
	}
	out := make([]appJSON, 0, len(grants))
	for _, g := range grants {
		cl := byID[g.ClientID]
		status := "connected"
		if g.RevokedAt != nil {
			status = "revoked"
		}
		scopes := splitScopes(g.Scopes)
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, appJSON{g.ID, g.ClientID, cl.Name, redirectHost(cl.RedirectURIs), scopes, status,
			g.RequestCount, g.CreatedAt, g.UpdatedAt, g.LastUsedAt, g.RevokedAt})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// redirectHost shows where an application receives its authorizations, which helps recognise it.
func redirectHost(uris string) string {
	first, _, _ := strings.Cut(uris, "\n")
	scheme, rest, ok := strings.Cut(first, "://")
	if !ok {
		return first
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host
}

// revokeGrant cuts an application off: the grant is marked revoked and every token it holds is deleted, in one
// transaction. Other applications and API keys are untouched.
func (a *App) revokeGrant(tx *gorm.DB, grantID string) error {
	if err := tx.Model(&models.OAuthGrant{}).Where("id = ? AND revoked_at IS NULL", grantID).Update("revoked_at", a.now()).Error; err != nil {
		return err
	}
	return tx.Where("grant_id = ?", grantID).Delete(&models.OAuthToken{}).Error
}

func (a *App) revokeApp(c *gin.Context) {
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var g models.OAuthGrant
		if err := tx.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&g).Error; err != nil {
			return notFound("That application")
		}
		return a.revokeGrant(tx, g.ID)
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) deleteApp(c *gin.Context) {
	user := current(c)
	var g models.OAuthGrant
	if err := a.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&g).Error; err != nil {
		fail(c, notFound("That application"))
		return
	}
	if g.RevokedAt == nil {
		fail(c, badRequest("Disconnect the application before removing it from the list."))
		return
	}
	if err := a.DB.Delete(&g).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
