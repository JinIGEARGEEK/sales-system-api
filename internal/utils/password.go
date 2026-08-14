package utils

import (
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// NewTempPassword generates a random password for Admin-created accounts that
// don't specify one; the account holder is expected to reset it via a future
// forgot-password flow (out of scope for this spec).
func NewTempPassword() string {
	return uuid.NewString()
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
