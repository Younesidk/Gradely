package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/engine"
	"github.com/younesidk/gradely/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	sessionCookie      = "gradely_session"
	rememberDuration   = 14 * 24 * time.Hour
	shortSessionLength = 12 * time.Hour
	maxFailedLogins    = 5
	lockoutDuration    = 10 * time.Minute
	userKey            = "gradely.user"

	genericLoginError = "Sign-in failed. Check your username/email and password. If this keeps happening your account may be locked or disabled."
	invalidInvitation = "This invitation link is invalid, has expired or was already used."
)

// ---------- tokens ----------

// newToken returns a cryptographically secure URL-safe token (256 bits).
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---------- sessions ----------

func (a *App) isHTTPS(c *gin.Context) bool {
	return a.Cfg.SecureCookies || c.Request.TLS != nil ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") ||
		strings.Contains(strings.ReplaceAll(c.GetHeader("Cf-Visitor"), " ", ""), `"scheme":"https"`)
}

func (a *App) startSession(c *gin.Context, userID string, remember bool) error {
	token := newToken()
	length := shortSessionLength
	if remember {
		length = rememberDuration
	}
	now := a.now()
	a.DB.Where("expires_at < ?", now).Delete(&models.Session{}) // tidy up
	if err := a.DB.Create(&models.Session{TokenHash: hashToken(token), UserID: userID, ExpiresAt: now.Add(length), CreatedAt: now}).Error; err != nil {
		return err
	}
	maxAge := 0 // session cookie
	if remember {
		maxAge = int(rememberDuration.Seconds())
	}
	a.setCookie(c, token, maxAge)
	return nil
}

func (a *App) setCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: a.isHTTPS(c), SameSite: http.SameSiteLaxMode,
	})
}

// loadSession resolves the signed-in user from the session cookie. With required it answers 401 when there is none.
// Disabled accounts and expired sessions are treated as signed out, so disabling a user takes effect immediately.
func (a *App) loadSession(required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := a.userFromCookie(c); u != nil {
			c.Set(userKey, u)
		} else if required {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Sign in required."})
			return
		}
		c.Next()
	}
}

func (a *App) userFromCookie(c *gin.Context) *models.User {
	token, err := c.Cookie(sessionCookie)
	if err != nil || token == "" || len(token) > 200 {
		return nil
	}
	var s models.Session
	if err := a.DB.Where("token_hash = ? AND expires_at > ?", hashToken(token), a.now()).First(&s).Error; err != nil {
		return nil
	}
	var u models.User
	if err := a.DB.First(&u, "id = ?", s.UserID).Error; err != nil || u.IsDisabled {
		return nil
	}
	return &u
}

func current(c *gin.Context) *models.User {
	u, _ := c.Get(userKey)
	user, _ := u.(*models.User)
	return user
}

func (a *App) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := current(c); u == nil || !u.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Administrator access is required."})
			return
		}
		c.Next()
	}
}

// ---------- validation ----------

func normalize(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// validEmail accepts plain addresses ("name@host"), like the invitation form always did.
func validEmail(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	p, err := mail.ParseAddress(s)
	return err == nil && p.Address == s
}

const allowedUserNameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._@+"

func validateUserName(name string) error {
	if len([]rune(name)) < 3 || len([]rune(name)) > 40 {
		return badRequest("Usernames are 3 to 40 characters.")
	}
	for _, r := range name {
		if !strings.ContainsRune(allowedUserNameChars, r) {
			return badRequest("Usernames can only contain letters, digits and - . _ @ +")
		}
	}
	return nil
}

func validatePassword(pw string) error {
	if len(pw) < 10 {
		return badRequest("Passwords must be at least 10 characters.")
	}
	hasDigit, hasLower := false, false
	for _, r := range pw {
		hasDigit = hasDigit || unicode.IsDigit(r)
		hasLower = hasLower || unicode.IsLower(r)
	}
	if !hasDigit {
		return badRequest("Passwords must have at least one digit ('0'-'9').")
	}
	if !hasLower {
		return badRequest("Passwords must have at least one lowercase letter ('a'-'z').")
	}
	return nil
}

// ---------- provisioning ----------

// createUser creates an account and its starter formulas. It runs inside tx.
func (a *App) createUser(tx *gorm.DB, email, userName, password string, isAdmin bool) (*models.User, error) {
	userName = strings.TrimSpace(userName)
	email = strings.TrimSpace(email)
	if err := validateUserName(userName); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	var taken int64
	tx.Model(&models.User{}).Where("normalized_user_name = ?", normalize(userName)).Count(&taken)
	if taken > 0 {
		return nil, badRequest("That username is already taken.")
	}
	tx.Model(&models.User{}).Where("normalized_email = ?", normalize(email)).Count(&taken)
	if taken > 0 {
		return nil, &userError{"This invitation can't be used.", http.StatusBadRequest}
	}
	hash, err := a.hashPassword(password)
	if err != nil {
		return nil, err
	}
	now := a.now()
	u := &models.User{
		UserName: userName, NormalizedUserName: normalize(userName),
		Email: email, NormalizedEmail: normalize(email),
		EmailConfirmed: true, // the address was supplied by the administrator's invitation / bootstrap config
		PasswordHash:   hash, IsAdmin: isAdmin, CreatedAt: now,
	}
	if err := tx.Create(u).Error; err != nil {
		return nil, err
	}
	if err := a.seedFormulas(tx, u.ID, now); err != nil {
		return nil, err
	}
	return u, nil
}

// seedFormulas gives a brand-new account its starter formulas.
func (a *App) seedFormulas(tx *gorm.DB, userID string, now time.Time) error {
	if err := tx.Create(&models.Formula{
		UserID: userID, Name: defaultNoLabName, Expression: engine.DefaultNoLabFormula,
		ForLabExam: false, IsDefault: true, UpdatedAt: now,
	}).Error; err != nil {
		return err
	}
	if lab := platformLabFormula(tx); lab != "" {
		return tx.Create(&models.Formula{
			UserID: userID, Name: defaultLabName, Expression: lab, ForLabExam: true, IsDefault: true, UpdatedAt: now,
		}).Error
	}
	return nil
}

// ---------- handlers ----------

type userDTO struct {
	ID       string `json:"id"`
	UserName string `json:"userName"`
	Email    string `json:"email"`
	IsAdmin  bool   `json:"isAdmin"`
}

func toUserDTO(u *models.User) userDTO {
	return userDTO{ID: u.ID, UserName: u.UserName, Email: u.Email, IsAdmin: u.IsAdmin}
}

func (a *App) me(c *gin.Context) {
	u := current(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Sign in required."})
		return
	}
	c.JSON(http.StatusOK, toUserDTO(u))
}

func (a *App) login(c *gin.Context) {
	var in struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
		Remember   bool   `json:"remember"`
	}
	if !bind(c, &in) {
		return
	}
	id := normalize(in.Identifier)
	var u models.User
	err := a.DB.Where("normalized_email = ? OR normalized_user_name = ?", id, id).First(&u).Error
	if err != nil {
		bcrypt.CompareHashAndPassword([]byte(a.dummyHash()), []byte(in.Password)) // keep timing similar to a real attempt
		fail(c, &userError{genericLoginError, http.StatusUnauthorized})
		return
	}
	now := a.now()
	if u.IsDisabled || (u.LockoutEnd != nil && u.LockoutEnd.After(now)) {
		fail(c, &userError{genericLoginError, http.StatusUnauthorized})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		u.FailedLogins++
		updates := map[string]any{"failed_logins": u.FailedLogins}
		if u.FailedLogins >= maxFailedLogins {
			updates["failed_logins"] = 0
			updates["lockout_end"] = now.Add(lockoutDuration)
		}
		a.DB.Model(&models.User{}).Where("id = ?", u.ID).Updates(updates)
		fail(c, &userError{genericLoginError, http.StatusUnauthorized})
		return
	}
	a.DB.Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{"failed_logins": 0, "lockout_end": nil, "last_login_at": now})
	if err := a.startSession(c, u.ID, in.Remember); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(&u))
}

func (a *App) dummyHash() string {
	if a.dummy == "" {
		a.dummy, _ = a.hashPassword("not-a-real-password-0")
	}
	return a.dummy
}

func (a *App) logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookie); err == nil && token != "" {
		a.DB.Where("token_hash = ?", hashToken(token)).Delete(&models.Session{})
	}
	a.setCookie(c, "", -1)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// invitedEmail returns the invited address if the token is currently redeemable.
func (a *App) invitedEmail(token string) (string, bool) {
	if strings.TrimSpace(token) == "" || len(token) > 200 {
		return "", false
	}
	var inv models.Invitation
	err := a.DB.Where("token_hash = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", hashToken(token), a.now()).First(&inv).Error
	if err != nil {
		return "", false
	}
	return inv.Email, true
}

func (a *App) invitationInfo(c *gin.Context) {
	email, ok := a.invitedEmail(c.Query("token"))
	if !ok {
		fail(c, &userError{invalidInvitation, http.StatusNotFound})
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": email})
}

// register creates an account from an invitation. A single transaction makes the token single-use.
func (a *App) register(c *gin.Context) {
	var in struct {
		Token    string `json:"token"`
		UserName string `json:"userName"`
		Password string `json:"password"`
	}
	if !bind(c, &in) {
		return
	}
	if strings.TrimSpace(in.Token) == "" || len(in.Token) > 200 {
		fail(c, badRequest(invalidInvitation))
		return
	}
	if strings.TrimSpace(in.UserName) == "" || in.Password == "" {
		fail(c, badRequest("Choose a username and a password."))
		return
	}
	hash := hashToken(in.Token)
	now := a.now()

	var created *models.User
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var inv models.Invitation
		if err := tx.Where("token_hash = ?", hash).First(&inv).Error; err != nil {
			return badRequest(invalidInvitation)
		}
		// Atomic claim: only one caller can flip redeemed_at from NULL.
		res := tx.Model(&models.Invitation{}).
			Where("id = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", inv.ID, now).
			Update("redeemed_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return badRequest(invalidInvitation)
		}
		u, err := a.createUser(tx, inv.Email, in.UserName, in.Password, false)
		if err != nil {
			return err // rolls the claim back: the invitation stays usable
		}
		created = u
		return tx.Model(&models.Invitation{}).Where("id = ?", inv.ID).Update("redeemed_by_user", u.ID).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	if err := a.startSession(c, created.ID, false); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(created))
}
