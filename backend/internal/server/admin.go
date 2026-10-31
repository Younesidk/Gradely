package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	gomail "github.com/younesidk/gradely/internal/mail"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type adminUserRow struct {
	ID           string    `json:"id"`
	UserName     string    `json:"userName"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"createdAt"`
	IsDisabled   bool      `json:"isDisabled"`
	IsAdmin      bool      `json:"isAdmin"`
	Semesters    int       `json:"semesters"`
	Modules      int       `json:"modules"`
	StorageBytes int64     `json:"storageBytes"`
}

func (a *App) userRows(users []models.User, report *storageReport) []adminUserRow {
	rows := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		var s, m int64
		a.DB.Model(&models.Semester{}).Where("user_id = ?", u.ID).Count(&s)
		a.DB.Model(&models.Module{}).Where("user_id = ?", u.ID).Count(&m)
		rows = append(rows, adminUserRow{u.ID, u.UserName, u.Email, u.CreatedAt, u.IsDisabled, u.IsAdmin,
			int(s), int(m), report.PerUser[u.ID].Bytes})
	}
	return rows
}

func (a *App) adminOverview(c *gin.Context) {
	report, err := a.storage.get(a, c.Query("refresh") == "1")
	if err != nil {
		fail(c, err)
		return
	}
	now := a.now()
	var users, disabled, pending, semesters, modules int64
	a.DB.Model(&models.User{}).Count(&users)
	a.DB.Model(&models.User{}).Where("is_disabled = ?", true).Count(&disabled)
	a.DB.Model(&models.Invitation{}).Where("redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", now).Count(&pending)
	a.DB.Model(&models.Semester{}).Count(&semesters)
	a.DB.Model(&models.Module{}).Count(&modules)

	topIDs := report.largest(5)
	var top []models.User
	if len(topIDs) > 0 {
		a.DB.Where("id IN ?", topIDs).Find(&top)
	}
	rows := a.userRows(top, report)
	for i := 0; i < len(rows); i++ { // largest first
		for j := i + 1; j < len(rows); j++ {
			if rows[j].StorageBytes > rows[i].StorageBytes {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"users": users, "activeUsers": users - disabled, "disabledUsers": disabled, "pendingInvitations": pending,
		"semesters": semesters, "modules": modules, "storage": report, "largestUsers": rows,
		"defaultLabFormula": nilIfEmpty(platformLabFormula(a.DB)),
	})
}

func (a *App) adminSetDefaultLabFormula(c *gin.Context) {
	var in struct {
		Expression string `json:"expression"`
	}
	if !bind(c, &in) {
		return
	}
	expression := strings.TrimSpace(in.Expression)
	if expression == "" {
		a.DB.Delete(&models.Setting{}, "key = ?", models.SettingDefaultLabFormula)
	} else {
		if msg := validateExpression(expression, true); msg != "" {
			fail(c, badRequest(msg))
			return
		}
		if err := a.DB.Save(&models.Setting{Key: models.SettingDefaultLabFormula, Value: expression}).Error; err != nil {
			fail(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (a *App) adminUsers(c *gin.Context) {
	pageSize := 10
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	q := a.DB.Model(&models.User{})
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + escapeLike(strings.ToLower(search)) + "%"
		q = q.Where(`LOWER(user_name) LIKE ? ESCAPE '\' OR LOWER(email) LIKE ? ESCAPE '\'`, like, like)
	}
	var total int64
	q.Count(&total)
	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	var users []models.User
	if err := q.Order("user_name").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error; err != nil {
		fail(c, err)
		return
	}
	report, err := a.storage.get(a, false)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": a.userRows(users, report), "page": page, "pageSize": pageSize, "total": total, "totalPages": pages})
}

func (a *App) adminUser(c *gin.Context) {
	var u models.User
	if err := a.DB.First(&u, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, notFound("That user"))
		return
	}
	report, err := a.storage.get(a, false)
	if err != nil {
		fail(c, err)
		return
	}
	var formulas, pending int64
	a.DB.Model(&models.Formula{}).Where("user_id = ?", u.ID).Count(&formulas)
	a.DB.Model(&models.Invitation{}).
		Where("normalized_email = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", u.NormalizedEmail, a.now()).Count(&pending)
	// Read-only inspection of another user's academic data (administrators only).
	semesters, err := a.loadSemesters(u.ID, "")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"row": a.userRows([]models.User{u}, report)[0], "lastLoginAt": u.LastLoginAt, "disabledAt": u.DisabledAt,
		"formulas": formulas, "pendingInvitationsForEmail": pending, "semesters": semesters,
	})
}

func (a *App) adminUserSemester(c *gin.Context) {
	var u models.User
	if err := a.DB.First(&u, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, notFound("That user"))
		return
	}
	semesters, err := a.loadSemesters(u.ID, c.Param("sid"))
	if err != nil {
		fail(c, err)
		return
	}
	if len(semesters) == 0 {
		fail(c, notFound("That semester"))
		return
	}
	report, err := a.storage.get(a, false)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": a.userRows([]models.User{u}, report)[0], "view": semesters[0]})
}

func (a *App) adminSetDisabled(c *gin.Context) {
	var in struct {
		Disabled bool `json:"disabled"`
	}
	if !bind(c, &in) {
		return
	}
	admin := current(c)
	if in.Disabled && c.Param("id") == admin.ID {
		fail(c, badRequest("You can't disable your own account."))
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.First(&u, "id = ?", c.Param("id")).Error; err != nil {
			return notFound("That user")
		}
		if in.Disabled && u.IsAdmin {
			return badRequest("Administrator accounts can't be disabled.")
		}
		if u.IsDisabled == in.Disabled {
			return nil
		}
		updates := map[string]any{"is_disabled": in.Disabled, "disabled_at": nil}
		if in.Disabled {
			updates["disabled_at"] = a.now()
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
			return err
		}
		// Ending every session makes the change take effect immediately.
		return tx.Delete(&models.Session{}, "user_id = ?", u.ID).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- invitations ----------

type invitationRow struct {
	ID          string                  `json:"id"`
	Email       string                  `json:"email"`
	Status      models.InvitationStatus `json:"status"`
	CreatedAt   time.Time               `json:"createdAt"`
	ExpiresAt   time.Time               `json:"expiresAt"`
	RedeemedAt  *time.Time              `json:"redeemedAt"`
	RevokedAt   *time.Time              `json:"revokedAt"`
	EmailSentAt *time.Time              `json:"emailSentAt"`
}

func toInvitationRow(i models.Invitation, now time.Time) invitationRow {
	return invitationRow{i.ID, i.Email, i.Status(now), i.CreatedAt, i.ExpiresAt, i.RedeemedAt, i.RevokedAt, i.EmailSentAt}
}

func (a *App) listInvitations(c *gin.Context) {
	var list []models.Invitation
	if err := a.DB.Order("created_at DESC").Limit(500).Find(&list).Error; err != nil {
		fail(c, err)
		return
	}
	now := a.now()
	rows := make([]invitationRow, 0, len(list))
	for _, i := range list {
		rows = append(rows, toInvitationRow(i, now))
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "emailConfigured": a.Mail.Configured()})
}

// issue gives an invitation a fresh single-use token and expiry. The raw token is returned, never stored.
func (a *App) issue(inv *models.Invitation, now time.Time) string {
	token := newToken()
	inv.TokenHash = hashToken(token)
	inv.CreatedAt = now
	inv.ExpiresAt = now.AddDate(0, 0, a.Cfg.InvitationValidDays)
	inv.RevokedAt = nil
	return token
}

// finishInvitation builds the link and emails it when delivery is configured.
func (a *App) finishInvitation(c *gin.Context, inv *models.Invitation, token string) gin.H {
	link := a.Cfg.BaseURL + "/account/register?token=" + token
	out := gin.H{"invitation": toInvitationRow(*inv, a.now()), "url": link, "emailSent": nil, "emailError": nil}
	if a.Mail.Configured() {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		if err := a.Mail.Send(ctx, gomail.Invitation(inv.Email, link, inv.ExpiresAt)); err != nil {
			out["emailSent"], out["emailError"] = false, err.Error()
		} else {
			now := a.now()
			inv.EmailSentAt = &now
			a.DB.Model(&models.Invitation{}).Where("id = ?", inv.ID).Update("email_sent_at", now)
			out["emailSent"] = true
			out["invitation"] = toInvitationRow(*inv, a.now())
		}
	}
	return out
}

func (a *App) createInvitation(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	if !bind(c, &in) {
		return
	}
	email := strings.TrimSpace(in.Email)
	if !validEmail(email) {
		fail(c, badRequest("Enter a valid email address."))
		return
	}
	normalized := normalize(email)
	var exists int64
	a.DB.Model(&models.User{}).Where("normalized_email = ?", normalized).Count(&exists)
	if exists > 0 {
		fail(c, badRequest("An account with that email already exists."))
		return
	}
	now := a.now()
	a.DB.Model(&models.Invitation{}).
		Where("normalized_email = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", normalized, now).Count(&exists)
	if exists > 0 {
		fail(c, badRequest("There is already a pending invitation for that address. Regenerate or revoke it instead."))
		return
	}
	admin := current(c).ID
	inv := models.Invitation{Email: email, NormalizedEmail: normalized, CreatedByUserID: &admin}
	token := a.issue(&inv, now)
	if err := a.DB.Create(&inv).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, a.finishInvitation(c, &inv, token))
}

// regenerateInvitation issues a fresh link for a pending or expired invitation (the previous link stops working).
func (a *App) regenerateInvitation(c *gin.Context) {
	var inv models.Invitation
	if err := a.DB.First(&inv, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, notFound("That invitation"))
		return
	}
	now := a.now()
	switch inv.Status(now) {
	case models.InvitationRedeemed:
		fail(c, badRequest("This invitation has already been used."))
		return
	case models.InvitationRevoked:
		fail(c, badRequest("This invitation was revoked. Create a new one instead."))
		return
	}
	var exists int64
	a.DB.Model(&models.User{}).Where("normalized_email = ?", inv.NormalizedEmail).Count(&exists)
	if exists > 0 {
		fail(c, badRequest("An account with that email already exists."))
		return
	}
	token := a.issue(&inv, now)
	inv.EmailSentAt = nil
	if err := a.DB.Save(&inv).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, a.finishInvitation(c, &inv, token))
}

func (a *App) revokeInvitation(c *gin.Context) {
	var inv models.Invitation
	if err := a.DB.First(&inv, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, notFound("That invitation"))
		return
	}
	now := a.now()
	if inv.Status(now) != models.InvitationPending {
		fail(c, badRequest("Only pending invitations can be revoked."))
		return
	}
	if err := a.DB.Model(&inv).Update("revoked_at", now).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
