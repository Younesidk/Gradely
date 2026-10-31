// Package models defines the GORM entities.
package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewID returns a fresh random identifier.
func NewID() string { return uuid.NewString() }

// User is an account. Administrators are flagged with IsAdmin.
type User struct {
	ID                 string `gorm:"primaryKey;size:36"`
	UserName           string `gorm:"size:64;not null"`
	NormalizedUserName string `gorm:"size:64;not null;uniqueIndex"`
	Email              string `gorm:"size:256;not null"`
	NormalizedEmail    string `gorm:"size:256;not null;uniqueIndex"`
	EmailConfirmed     bool
	PasswordHash       string `gorm:"size:100;not null"`
	IsAdmin            bool
	IsDisabled         bool
	DisabledAt         *time.Time
	LastLoginAt        *time.Time
	FailedLogins       int
	LockoutEnd         *time.Time
	CreatedAt          time.Time

	Semesters []Semester `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Formulas  []Formula  `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// Session is a server-side login session. Only the SHA-256 of the cookie token is stored.
type Session struct {
	TokenHash string    `gorm:"primaryKey;size:64"`
	UserID    string    `gorm:"size:36;not null;index"`
	ExpiresAt time.Time `gorm:"not null;index"`
	CreatedAt time.Time
}

type Semester struct {
	ID           string  `gorm:"primaryKey;size:36"`
	UserID       string  `gorm:"size:36;not null;index"`
	Name         string  `gorm:"size:100;not null"`
	AcademicYear *string `gorm:"size:50"`
	StartDate    *string `gorm:"size:10"` // YYYY-MM-DD
	EndDate      *string `gorm:"size:10"`
	SortOrder    int
	CreatedAt    time.Time
	UpdatedAt    time.Time

	Modules []Module `gorm:"foreignKey:SemesterID;constraint:OnDelete:CASCADE"`
}

type Module struct {
	ID         string `gorm:"primaryKey;size:36"`
	SemesterID string `gorm:"size:36;not null;index"`
	// UserID is denormalised so every module query can filter by owner directly.
	UserID           string  `gorm:"size:36;not null;index"`
	Name             string  `gorm:"size:100;not null"`
	Code             *string `gorm:"size:30"`
	Coefficient      float64
	MaxGrade         float64
	PassingThreshold float64
	HasLabExam       bool

	// nil = not entered yet. 0 = an actual zero.
	CC1       *float64
	CC2       *float64
	LabGrade  *float64
	ExamGrade *float64

	FormulaID *string `gorm:"size:36;index"`
	SortOrder int
	UpdatedAt time.Time
}

// Formula is a user's grading formula.
type Formula struct {
	ID         string `gorm:"primaryKey;size:36"`
	UserID     string `gorm:"size:36;not null;index"`
	Name       string `gorm:"size:80;not null"`
	Expression string `gorm:"size:500;not null"`
	ForLabExam bool
	IsDefault  bool
	UpdatedAt  time.Time
}

type Invitation struct {
	ID              string `gorm:"primaryKey;size:36"`
	Email           string `gorm:"size:256;not null"`
	NormalizedEmail string `gorm:"size:256;not null;index"`
	// TokenHash is the SHA-256 (hex) of the invitation token. The raw token is never stored.
	TokenHash       string `gorm:"size:64;not null;uniqueIndex"`
	CreatedAt       time.Time
	ExpiresAt       time.Time
	RedeemedAt      *time.Time
	RevokedAt       *time.Time
	EmailSentAt     *time.Time
	CreatedByUserID *string `gorm:"size:36"`
	RedeemedByUser  *string `gorm:"size:36"`
}

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "Pending"
	InvitationRedeemed InvitationStatus = "Redeemed"
	InvitationExpired  InvitationStatus = "Expired"
	InvitationRevoked  InvitationStatus = "Revoked"
)

func (i Invitation) Status(now time.Time) InvitationStatus {
	switch {
	case i.RedeemedAt != nil:
		return InvitationRedeemed
	case i.RevokedAt != nil:
		return InvitationRevoked
	case !i.ExpiresAt.After(now):
		return InvitationExpired
	default:
		return InvitationPending
	}
}

// EmailChange is a pending, token-confirmed change of a user's email address.
type EmailChange struct {
	TokenHash string `gorm:"primaryKey;size:64"`
	UserID    string `gorm:"size:36;not null;index"`
	NewEmail  string `gorm:"size:256;not null"`
	ExpiresAt time.Time
}

// Setting is a small key/value table for platform-wide settings.
type Setting struct {
	Key   string `gorm:"primaryKey;size:100"`
	Value string
}

const (
	SettingAdminBootstrapped = "AdminBootstrappedAtUtc"
	SettingDefaultLabFormula = "DefaultLabFormula"
)

// All lists every entity for AutoMigrate.
func All() []any {
	return []any{&User{}, &Session{}, &Semester{}, &Module{}, &Formula{}, &Invitation{}, &EmailChange{}, &Setting{}}
}

// BeforeCreate fills in an ID when none was set.
func (u *User) BeforeCreate(*gorm.DB) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	return nil
}
func (s *Semester) BeforeCreate(*gorm.DB) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	return nil
}
func (m *Module) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	return nil
}
func (f *Formula) BeforeCreate(*gorm.DB) error {
	if f.ID == "" {
		f.ID = NewID()
	}
	return nil
}
func (i *Invitation) BeforeCreate(*gorm.DB) error {
	if i.ID == "" {
		i.ID = NewID()
	}
	return nil
}
