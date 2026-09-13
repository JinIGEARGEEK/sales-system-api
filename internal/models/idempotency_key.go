package models

import "time"

// IdempotencyKey lets an Open API caller safely retry a POST (e.g. after a
// timeout with an unknown outcome) without risking a duplicate Company/
// Contact — this CRM is meant to be the source of truth other internal
// systems sync customer/contact data through, so an accidental duplicate
// here isn't just clutter, it propagates outward to everything reading from
// it. Scoped per API key (the composite unique index), so two different
// integrations coincidentally reusing the same key value never collide.
// See middleware.RequireIdempotency for how this is claimed/replayed.
type IdempotencyKey struct {
	ID          uint   `gorm:"primaryKey" json:"-"`
	APIKeyID    uint   `gorm:"not null;uniqueIndex:idx_idempotency_scope" json:"-"`
	Key         string `gorm:"not null;uniqueIndex:idx_idempotency_scope" json:"-"`
	Method      string `gorm:"not null" json:"-"`
	Path        string `gorm:"not null" json:"-"`
	RequestHash string `gorm:"not null" json:"-"`
	// ResponseStatus is 0 while the original request is still in flight —
	// middleware.RequireIdempotency relies on that zero value to tell "another
	// request already claimed this key and is still running" apart from "a
	// completed response is cached here".
	ResponseStatus int       `json:"-"`
	ResponseBody   []byte    `gorm:"type:bytea" json:"-"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index" json:"-"`
}

func (IdempotencyKey) TableName() string { return "idempotency_keys" }
