package server

import (
	"log/slog"

	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

// BootstrapAdmin creates the first administrator from ADMIN_EMAIL / ADMIN_USERNAME / ADMIN_PASSWORD
// (and an optional DEFAULT_LAB_FORMULA). It runs once: afterwards a marker is stored and the account is never
// recreated or overwritten.
func (a *App) BootstrapAdmin() error {
	var marker int64
	a.DB.Model(&models.Setting{}).Where("key = ?", models.SettingAdminBootstrapped).Count(&marker)
	if marker > 0 {
		return nil
	}
	markDone := func() error {
		return a.DB.Save(&models.Setting{Key: models.SettingAdminBootstrapped, Value: a.now().Format("2006-01-02T15:04:05Z")}).Error
	}

	var admins int64
	a.DB.Model(&models.User{}).Where("is_admin = ?", true).Count(&admins)
	if admins > 0 {
		return markDone() // an administrator already exists; never touch it
	}

	c := a.Cfg
	if c.AdminEmail == "" || c.AdminUsername == "" || c.AdminPassword == "" {
		slog.Warn("No administrator exists yet. Set ADMIN_EMAIL, ADMIN_USERNAME and ADMIN_PASSWORD (and restart) to create the first administrator.")
		return nil
	}

	// Optional first-run default lab formula (editable later in Administration).
	if lab := c.DefaultLabFormula; lab != "" {
		if msg := validateExpression(lab, true); msg != "" {
			slog.Warn("DEFAULT_LAB_FORMULA was ignored because it is invalid", "error", msg)
		} else if platformLabFormula(a.DB) == "" {
			a.DB.Save(&models.Setting{Key: models.SettingDefaultLabFormula, Value: lab})
		}
	}

	err := a.DB.Transaction(func(tx *gorm.DB) error {
		_, err := a.createUser(tx, c.AdminEmail, c.AdminUsername, c.AdminPassword, true)
		return err
	})
	if err != nil {
		// Never log the password.
		slog.Error("Could not create the initial administrator", "error", err.Error())
		return nil
	}
	slog.Info("Initial administrator created", "username", c.AdminUsername)
	return markDone()
}
