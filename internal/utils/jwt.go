package utils

import (
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/igeargeek/sales-system-api/internal/models"
)

type Claims struct {
	UserID uint        `json:"user_id"`
	Role   models.Role `json:"role"`
	// TokenVersion snapshots models.User.TokenVersion at issuance time.
	// RequireAuth rejects the token once it no longer matches the DB value —
	// see models.User.TokenVersion's doc for why (logout/forced-logout).
	TokenVersion int `json:"token_version"`
	jwt.RegisteredClaims
}

func GenerateToken(secret string, expiryHours int, userID uint, role models.Role, tokenVersion int) (string, error) {
	claims := Claims{
		UserID:       userID,
		Role:         role,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expiryHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ParseToken(secret, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		return nil, err
	}
	return claims, nil
}
