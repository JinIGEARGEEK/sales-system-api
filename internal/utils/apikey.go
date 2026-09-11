package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// APIKeyPrefix marks every generated key as belonging to this API (mirrors
// the convention of Stripe/GitHub-style prefixed secrets) so a key pasted
// into a log or bug report is instantly recognizable as this system's, not
// merely an opaque hex blob.
const APIKeyPrefix = "sk_live_"

// GenerateAPIKey returns a new random raw key (shown to the caller exactly
// once) and its sha256 hash (what's actually persisted — see
// models.APIKey.KeyHash). 32 random bytes hex-encoded gives 64 hex chars of
// entropy after the prefix, comfortably unguessable.
func GenerateAPIKey() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate api key: %w", err)
	}
	raw = APIKeyPrefix + hex.EncodeToString(buf)
	hash = HashAPIKey(raw)
	return raw, hash, nil
}

// HashAPIKey is a plain sha256 (not bcrypt): unlike a user password, an API
// key is already high-entropy random data rather than something a human
// picked, so it isn't at risk from a fast dictionary/brute-force attack —
// sha256 keeps the per-request lookup (middleware.RequireAPIKey) a cheap
// indexed equality match instead of bcrypt's deliberately-slow compare.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// APIKeyPrefixLen is how much of the raw key is retained as models.APIKey.KeyPrefix
// for display — long enough to include the full "sk_live_" marker plus a few
// bytes of the secret so an Admin can tell same-named keys apart.
const APIKeyPrefixLen = len(APIKeyPrefix) + 6
