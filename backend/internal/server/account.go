package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	gomail "github.com/younesidk/gradely/internal/mail"
	"github.com/younesidk/gradely/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const emailChangeValid = 24 * time.Hour

func (a *App) getAccount(c *gin.Context) {
	u := current(c)
	c.JSON(http.StatusOK, gin.H{
		"userName": u.UserName, "email": u.Email, "emailConfirmed": u.EmailConfirmed,
		"createdAt": u.CreatedAt, "isAdmin": u.IsAdmin,
	})
}

func (a *App) changeUserName(c *gin.Context) {
	var in struct {
		UserName string `json:"userName"`
	}
	if !bind(c, &in) {
		return
	}
	name := strings.TrimSpace(in.UserName)
	if name == "" {
		fail(c, badRequest("Enter a username."))
		return
	}
	u := current(c)
	if name == u.UserName {
		c.JSON(http.StatusOK, gin.H{"userName": u.UserName})
		return
	}
	if err := validateUserName(name); err != nil {
		fail(c, err)
		return
	}
	var taken int64
	a.DB.Model(&models.User{}).Where("normalized_user_name = ? AND id <> ?", normalize(name), u.ID).Count(&taken)
	if taken > 0 {
		fail(c, badRequest("That username is already taken."))
		return
	}
	if err := a.DB.Model(&models.User{}).Where("id = ?", u.ID).
		Updates(map[string]any{"user_name": name, "normalized_user_name": normalize(name)}).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"userName": name})
}

// requestEmailChange starts an email change: the address only changes once the link sent to the NEW address is
// opened. When email delivery isn't configured the link is returned so it can be opened manually. The result never
// says whether the address already belongs to another account.
func (a *App) requestEmailChange(c *gin.Context) {
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
	u := current(c)
	if strings.EqualFold(u.Email, email) {
		fail(c, badRequest("That is already your email address."))
		return
	}

	token := newToken()
	now := a.now()
	a.DB.Where("user_id = ?", u.ID).Delete(&models.EmailChange{}) // only the latest request counts
	if err := a.DB.Create(&models.EmailChange{TokenHash: hashToken(token), UserID: u.ID, NewEmail: email, ExpiresAt: now.Add(emailChangeValid)}).Error; err != nil {
		fail(c, err)
		return
	}
	link := a.Cfg.BaseURL + "/account/confirm-email-change?token=" + url.QueryEscape(token)

	if a.Mail.Configured() {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		if err := a.Mail.Send(ctx, gomail.ConfirmEmailChange(email, link)); err != nil {
			fail(c, badRequest("We couldn't send the confirmation email. Try again later."))
			return
		}
		c.JSON(http.StatusOK, gin.H{"emailSent": true, "manualLink": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"emailSent": false, "manualLink": link})
}

func (a *App) confirmEmailChange(c *gin.Context) {
	var in struct {
		Token string `json:"token"`
	}
	if !bind(c, &in) {
		return
	}
	invalid := &userError{"This confirmation link is invalid or has expired. Request a new one from Account settings.", http.StatusBadRequest}
	if strings.TrimSpace(in.Token) == "" || len(in.Token) > 200 {
		fail(c, invalid)
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var ch models.EmailChange
		if err := tx.Where("token_hash = ? AND expires_at > ?", hashToken(in.Token), a.now()).First(&ch).Error; err != nil {
			return invalid
		}
		var u models.User
		if err := tx.First(&u, "id = ?", ch.UserID).Error; err != nil || u.IsDisabled {
			return invalid
		}
		var taken int64
		tx.Model(&models.User{}).Where("normalized_email = ? AND id <> ?", normalize(ch.NewEmail), u.ID).Count(&taken)
		if taken > 0 {
			return invalid
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).
			Updates(map[string]any{"email": ch.NewEmail, "normalized_email": normalize(ch.NewEmail), "email_confirmed": true}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.EmailChange{}, "token_hash = ?", ch.TokenHash).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// changePassword also signs the user out of every device.
func (a *App) changePassword(c *gin.Context) {
	var in struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !bind(c, &in) {
		return
	}
	if in.Current == "" || in.Next == "" {
		fail(c, badRequest("Enter your current and your new password."))
		return
	}
	u := current(c)
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Current)) != nil {
		fail(c, badRequest("Your current password is incorrect."))
		return
	}
	if err := validatePassword(in.Next); err != nil {
		fail(c, err)
		return
	}
	hash, err := a.hashPassword(in.Next)
	if err != nil {
		fail(c, err)
		return
	}
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Session{}, "user_id = ?", u.ID).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	a.setCookie(c, "", -1)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
