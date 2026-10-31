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

	APIKeys     []APIKey           `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	OAuthGrants []OAuthGrant       `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	OAuthCodes  []OAuthCode        `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Events      []IntegrationEvent `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
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

// ---------- developer integrations ----------

// APIKey is a personal key for the MCP endpoint. Only the SHA-256 of the secret is stored; Prefix is the
// non-secret start of the key, shown so people can tell their keys apart.
type APIKey struct {
	ID           string  `gorm:"primaryKey;size:36"`
	UserID       string  `gorm:"size:36;not null;index"`
	Name         string  `gorm:"size:80;not null"`
	Description  *string `gorm:"size:300"`
	Prefix       string  `gorm:"size:24;not null"`
	KeyHash      string  `gorm:"size:64;not null;uniqueIndex"`
	Scopes       string  `gorm:"size:400;not null"` // space-separated
	RequestCount int64
	CreatedAt    time.Time
	LastUsedAt   *time.Time
	ExpiresAt    *time.Time
	RotatedAt    *time.Time
	RevokedAt    *time.Time
}

// OAuthClient is an application registered through dynamic client registration (RFC 7591). Only public clients
// (PKCE, no client secret) are supported.
type OAuthClient struct {
	ID           string `gorm:"primaryKey;size:64"` // the client_id
	Name         string `gorm:"size:100;not null"`
	RedirectURIs string `gorm:"size:2600;not null"` // newline-separated, compared exactly
	CreatedAt    time.Time

	Grants []OAuthGrant `gorm:"foreignKey:ClientID;constraint:OnDelete:CASCADE"`
}

// OAuthGrant is one user's authorisation of one client: a "connected application". Its tokens die with it.
type OAuthGrant struct {
	ID           string `gorm:"primaryKey;size:36"`
	UserID       string `gorm:"size:36;not null;index"`
	ClientID     string `gorm:"size:64;not null;index"`
	Scopes       string `gorm:"size:400;not null"`
	Resource     string `gorm:"size:300;not null"`
	RequestCount int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time

	Tokens []OAuthToken `gorm:"foreignKey:GrantID;constraint:OnDelete:CASCADE"`
}

// OAuthCode is a single-use authorization code (stored hashed) bound to its client, redirect URI and PKCE challenge.
type OAuthCode struct {
	CodeHash      string    `gorm:"primaryKey;size:64"`
	UserID        string    `gorm:"size:36;not null;index"`
	ClientID      string    `gorm:"size:64;not null"`
	RedirectURI   string    `gorm:"size:500;not null"`
	Scopes        string    `gorm:"size:400;not null"`
	Resource      string    `gorm:"size:300;not null"`
	CodeChallenge string    `gorm:"size:128;not null"`
	ExpiresAt     time.Time `gorm:"not null;index"`
	UsedAt        *time.Time
	GrantID       *string `gorm:"size:36"` // the grant it produced, so a replayed code can revoke it
}

// OAuthToken is an access or refresh token (stored hashed).
type OAuthToken struct {
	TokenHash string     `gorm:"primaryKey;size:64"`
	Kind      string     `gorm:"size:8;not null"` // "access" or "refresh"
	GrantID   string     `gorm:"size:36;not null;index"`
	UserID    string     `gorm:"size:36;not null;index"`
	Scopes    string     `gorm:"size:400;not null"`
	Resource  string     `gorm:"size:300;not null"`
	ExpiresAt time.Time  `gorm:"not null;index"`
	UsedAt    *time.Time // refresh tokens: when they were rotated (a second use revokes the grant)
	CreatedAt time.Time
}

const (
	TokenAccess  = "access"
	TokenRefresh = "refresh"
)

// IntegrationEvent is one line of a user's integration activity log. It never holds secrets or academic values.
type IntegrationEvent struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	UserID         string    `gorm:"size:36;not null;index:idx_events_user_time,priority:1"`
	CreatedAt      time.Time `gorm:"index:idx_events_user_time,priority:2;index"`
	CredentialType string    `gorm:"size:16;not null"` // "api_key" or "oauth"
	CredentialID   string    `gorm:"size:36;not null"`
	CredentialName string    `gorm:"size:100;not null"`
	Operation      string    `gorm:"size:64;not null"`
	Category       string    `gorm:"size:16;not null"` // read, write, delete, limit
	Success        bool
	Status         int
	ErrorCode      *string `gorm:"size:32"`
	DurationMs     int64
}

// IdempotencyRecord remembers the result of a create call made with an idempotency key, so a retried call returns
// the first result instead of creating a duplicate. Records expire after a day.
type IdempotencyRecord struct {
	CredentialID string `gorm:"primaryKey;size:36"`
	Key          string `gorm:"primaryKey;size:100"`
	Operation    string `gorm:"size:64;not null"`
	Result       string `gorm:"size:20000"`
	Pending      bool
	CreatedAt    time.Time `gorm:"index"`
}

// All lists every entity for AutoMigrate.
func All() []any {
	return []any{&User{}, &Session{}, &Semester{}, &Module{}, &Formula{}, &Invitation{}, &EmailChange{}, &Setting{},
		&APIKey{}, &OAuthClient{}, &OAuthGrant{}, &OAuthCode{}, &OAuthToken{}, &IntegrationEvent{}, &IdempotencyRecord{}}
}

// IntegrationTables lists the tables added for developer integrations, newest dependants first. Dropping them in
// this order rolls the integrations schema back without touching academic data.
func IntegrationTables() []any {
	return []any{&IdempotencyRecord{}, &IntegrationEvent{}, &OAuthToken{}, &OAuthCode{}, &OAuthGrant{}, &OAuthClient{}, &APIKey{}}
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
func (k *APIKey) BeforeCreate(*gorm.DB) error {
	if k.ID == "" {
		k.ID = NewID()
	}
	return nil
}
func (g *OAuthGrant) BeforeCreate(*gorm.DB) error {
	if g.ID == "" {
		g.ID = NewID()
	}
	return nil
}
