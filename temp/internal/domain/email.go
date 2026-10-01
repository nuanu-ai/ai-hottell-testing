package domain

import (
	"strings"
	"unicode/utf8"
)

// Email is a user's login: trimmed and lower-cased, so it compares case-insensitively.
type Email string

const (
	emailMinLen = 3
	emailMaxLen = 254
)

// ParseEmail normalises s and checks it is an email address: 3–254 characters, exactly
// one @, non-empty parts on both sides, and a domain that has a dot or is localhost.
func ParseEmail(s string) (Email, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n := utf8.RuneCountInString(s); n < emailMinLen || n > emailMaxLen {
		return "", ErrInvalidEmail
	}
	local, host, ok := strings.Cut(s, "@")
	if !ok || local == "" || host == "" || strings.Contains(host, "@") {
		return "", ErrInvalidEmail
	}
	if !strings.Contains(host, ".") && host != "localhost" {
		return "", ErrInvalidEmail
	}
	return Email(s), nil
}

// String returns the normalised address.
func (e Email) String() string {
	return string(e)
}
