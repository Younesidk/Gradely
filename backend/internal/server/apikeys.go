package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

const maxActiveAPIKeys = 25

type apiKeyJSON struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Description  *string    `json:"description"`
	Prefix       string     `json:"prefix"`
	Scopes       []string   `json:"scopes"`
	Status       string     `json:"status"` // active, expired, revoked
	RequestCount int64      `json:"requestCount"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	RotatedAt    *time.Time `json:"rotatedAt"`
	RevokedAt    *time.Time `json:"revokedAt"`
}

func keyStatus(k models.APIKey, now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return "revoked"
	case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
		return "expired"
	default:
		return "active"
	}
}

func toAPIKeyJSON(k models.APIKey, now time.Time) apiKeyJSON {
	scopes := splitScopes(k.Scopes)
	if scopes == nil {
		scopes = []string{}
	}
	return apiKeyJSON{k.ID, k.Name, k.Description, k.Prefix + "…", scopes, keyStatus(k, now), k.RequestCount,
		k.CreatedAt, k.LastUsedAt, k.ExpiresAt, k.RotatedAt, k.RevokedAt}
}

type apiKeyForm struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Scopes      []string `json:"scopes"`
	ExpiresAt   *string  `json:"expiresAt"` // YYYY-MM-DD (valid through the end of that day, UTC) or RFC 3339; empty = never
}

func validateKeyForm(in *apiKeyForm) (name string, scopes []string, err error) {
	name = strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return "", nil, badRequest("Give the key a name, such as the app or script that will use it.")
	case len([]rune(name)) > 80:
		return "", nil, badRequest("The key name is too long (max 80).")
	case in.Description != nil && len([]rune(*in.Description)) > 300:
		return "", nil, badRequest("The description is too long (max 300).")
	}
	scopes, unknown := normalizeScopes(in.Scopes)
	if unknown != "" {
		return "", nil, badRequest("Unknown permission: " + unknown)
	}
	if len(scopes) == 0 {
		return "", nil, badRequest("Choose at least one permission.")
	}
	return name, scopes, nil
}

func (a *App) parseExpiry(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	var t time.Time
	if d, err := time.Parse("2006-01-02", v); err == nil {
		t = d.Add(24*time.Hour - time.Second) // valid through that whole day
	} else if ts, err := time.Parse(time.RFC3339, v); err == nil {
		t = ts.UTC()
	} else {
		return nil, badRequest("Enter the expiry date as YYYY-MM-DD.")
	}
	if !t.After(a.now()) {
		return nil, badRequest("The expiry date must be in the future.")
	}
	return &t, nil
}

func (a *App) ownedKey(tx *gorm.DB, userID, id string) (*models.APIKey, error) {
	var k models.APIKey
	if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&k).Error; err != nil {
		return nil, notFound("That API key")
	}
	return &k, nil
}

func (a *App) listAPIKeys(c *gin.Context) {
	var keys []models.APIKey
	if err := a.DB.Where("user_id = ?", current(c).ID).Order("revoked_at IS NOT NULL, created_at DESC").Find(&keys).Error; err != nil {
		fail(c, err)
		return
	}
	now := a.now()
	out := make([]apiKeyJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKeyJSON(k, now))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// createAPIKey answers with the full secret. This is the only time it exists outside the caller: only its hash is
// stored.
func (a *App) createAPIKey(c *gin.Context) {
	var in apiKeyForm
	if !bind(c, &in) {
		return
	}
	name, scopes, err := validateKeyForm(&in)
	if err != nil {
		fail(c, err)
		return
	}
	expires, err := a.parseExpiry(in.ExpiresAt)
	if err != nil {
		fail(c, err)
		return
	}
	user := current(c)
	secret := newSecret(apiKeyPrefix)
	now := a.now()
	k := models.APIKey{
		UserID: user.ID, Name: name, Description: clean(in.Description), Prefix: secret[:keyDisplayLength],
		KeyHash: hashToken(secret), Scopes: joinScopes(scopes), CreatedAt: now, ExpiresAt: expires,
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var active int64
		tx.Model(&models.APIKey{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Count(&active)
		if active >= maxActiveAPIKeys {
			return badRequest("You already have 25 API keys. Revoke one you no longer use first.")
		}
		return tx.Create(&k).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{"key": toAPIKeyJSON(k, now), "secret": secret})
}

func (a *App) updateAPIKey(c *gin.Context) {
	var in apiKeyForm
	if !bind(c, &in) {
		return
	}
	name, scopes, err := validateKeyForm(&in)
	if err != nil {
		fail(c, err)
		return
	}
	k, err := a.ownedKey(a.DB, current(c).ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if k.RevokedAt != nil {
		fail(c, badRequest("A revoked key can't be changed."))
		return
	}
	k.Name, k.Description, k.Scopes = name, clean(in.Description), joinScopes(scopes)
	if err := a.DB.Model(k).Select("name", "description", "scopes").Updates(k).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyJSON(*k, a.now()))
}

// rotateAPIKey swaps the secret in place: the old secret stops working in the same statement that stores the new
// hash, and the key keeps its name, permissions and history.
func (a *App) rotateAPIKey(c *gin.Context) {
	user := current(c)
	k, err := a.ownedKey(a.DB, user.ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	now := a.now()
	if keyStatus(*k, now) != "active" {
		fail(c, badRequest("Only active keys can be rotated. Create a new key instead."))
		return
	}
	secret := newSecret(apiKeyPrefix)
	res := a.DB.Model(&models.APIKey{}).Where("id = ? AND user_id = ? AND revoked_at IS NULL", k.ID, user.ID).
		Updates(map[string]any{"key_hash": hashToken(secret), "prefix": secret[:keyDisplayLength], "rotated_at": now})
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	if res.RowsAffected != 1 {
		fail(c, badRequest("Only active keys can be rotated. Create a new key instead."))
		return
	}
	k.Prefix, k.RotatedAt = secret[:keyDisplayLength], &now
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"key": toAPIKeyJSON(*k, now), "secret": secret})
}

func (a *App) revokeAPIKey(c *gin.Context) {
	user := current(c)
	k, err := a.ownedKey(a.DB, user.ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	now := a.now()
	if k.RevokedAt == nil {
		a.DB.Model(&models.APIKey{}).Where("id = ? AND revoked_at IS NULL", k.ID).Update("revoked_at", now)
		k.RevokedAt = &now
	}
	c.JSON(http.StatusOK, toAPIKeyJSON(*k, now))
}

func (a *App) deleteAPIKey(c *gin.Context) {
	user := current(c)
	k, err := a.ownedKey(a.DB, user.ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if keyStatus(*k, a.now()) == "active" {
		fail(c, badRequest("Revoke the key before deleting it."))
		return
	}
	if err := a.DB.Delete(k).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
