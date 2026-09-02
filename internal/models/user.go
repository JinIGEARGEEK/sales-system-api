package models

import "time"

// Role reconciles the spec's flagged mismatch (api-system-spec.md §2.1 note): the RBAC
// rules in §1.7 are defined against Admin/Sales Rep/Sales Manager/Production, so those
// are the roles actually enforced and stored here rather than the frontend's placeholder
// Admin/Editor/Viewer enum. Revisit if the product owner reconciles the naming.
type Role string

const (
	RoleAdmin        Role = "Admin"
	RoleSalesRep     Role = "Sales Rep"
	RoleSalesManager Role = "Sales Manager"
	RoleProduction   Role = "Production"
	// RoleMarketing owns the Prospect funnel (pre-Lead Company/Contact
	// outreach) — added 2026-09-01, not part of the original spec's §1.7 RBAC
	// table; see biz_spec/feature-spec.md's Prospect Management section.
	RoleMarketing Role = "Marketing"
)

// User is the staff account model — api-system-spec.md §2.1.
type User struct {
	AuditedModel
	FirstName         string `gorm:"not null" json:"first_name"`
	LastName          string `gorm:"not null" json:"last_name"`
	Tel               string `json:"tel"`
	Email             string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash      string `gorm:"not null" json:"-"`
	Role              Role   `gorm:"type:varchar(32);not null" json:"role"`
	Notes             string `json:"notes"`
	AcceptedConsentID *uint  `json:"accepted_consent_id"`
	IsActive          bool   `gorm:"default:true" json:"is_active"`
	// MustChangePassword is set whenever an Admin assigns this account's password
	// (creation or reset) and cleared once the holder sets their own via
	// POST /auth/change-password — see middleware.RequirePasswordChanged.
	MustChangePassword bool       `gorm:"default:false;not null" json:"must_change_password"`
	LatestLogin        *time.Time `json:"latest_login"`
	// TokenVersion is embedded in every JWT issued to this user (see
	// utils.GenerateToken/Claims) and compared against the DB value on every
	// request (middleware.RequireAuth). Bumping it — on logout, or an Admin
	// forcing one — instantly invalidates every previously-issued token for
	// this user without needing a server-side token blocklist.
	TokenVersion int `gorm:"default:0;not null" json:"-"`
}

func (User) TableName() string { return "users" }
