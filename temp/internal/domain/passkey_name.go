package domain

import (
	"strings"
	"unicode/utf8"
)

// PasskeyName is the name a user gives a passkey.
type PasskeyName string

const passkeyNameMaxLen = 64

// ParsePasskeyName trims s and checks it has 1–64 characters.
func ParsePasskeyName(s string) (PasskeyName, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > passkeyNameMaxLen {
		return "", ErrInvalidPasskeyName
	}
	return PasskeyName(s), nil
}

// String returns the name.
func (n PasskeyName) String() string {
	return string(n)
}
