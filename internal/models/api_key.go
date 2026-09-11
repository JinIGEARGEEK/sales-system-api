package models

import "time"

// APIKey authenticates external/integration callers on the /open/* group
// (see routes.Setup and middleware.RequireAPIKey) — a separate credential
// from the staff Bearer-JWT login flow, since a server-to-server integration
// has no user to log in as. Every key acts AS an existing staff User
// (OwnerUserID): that user's ID/Role populate the same c.Locals RequireAuth
// would, so Create/Update on Company/Contact still get a real created_by/
// updated_by and any future RequireRoles gate on these routes is honored,
// same as a normal request from that user.
type APIKey struct {
	SimpleModel
	Name string `gorm:"not null" json:"name"`
	// KeyHash is sha256(raw key), hex-encoded — the raw key is shown to the
	// caller exactly once, at creation (utils.GenerateAPIKey), and never
	// stored or logged in recoverable form, same principle as PasswordHash.
	KeyHash string `gorm:"uniqueIndex;not null" json:"-"`
	// KeyPrefix is the raw key's first utils.APIKeyPrefixLen chars (the
	// "sk_live_" marker plus a few bytes of the secret) — stored only so an
	// Admin can tell keys apart in the list UI without the full secret ever
	// touching the DB.
	KeyPrefix   string     `gorm:"not null" json:"key_prefix"`
	OwnerUserID uint       `gorm:"not null;index" json:"owner_user_id"`
	IsActive    bool       `gorm:"default:true;not null" json:"is_active"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedBy   *uint      `json:"created_by,omitempty"`
	// RevokedAt/RevokedBy record a soft revoke (IsActive=false) — kept
	// instead of deleting the row so a revoked key's audit trail (who made
	// it, when, who killed it) survives for the Admin API-key list.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	RevokedBy *uint      `json:"revoked_by,omitempty"`
}

func (APIKey) TableName() string { return "api_keys" }
