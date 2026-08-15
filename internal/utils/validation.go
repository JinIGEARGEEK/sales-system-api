package utils

import (
	"net/mail"
	"strings"
)

// AllowedUsernameDomain restricts staff usernames to the company email domain.
const AllowedUsernameDomain = "igeargeek.com"

// IsValidUsername reports whether username is a syntactically valid email
// address on AllowedUsernameDomain.
func IsValidUsername(username string) bool {
	addr, err := mail.ParseAddress(username)
	if err != nil || addr.Address != username {
		return false
	}
	at := strings.LastIndex(username, "@")
	if at < 0 {
		return false
	}
	return strings.EqualFold(username[at+1:], AllowedUsernameDomain)
}
