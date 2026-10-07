package domain

import (
	"strings"
	"unicode/utf8"
)

// UserName is the display name of a user.
type UserName string

const userNameMaxLen = 100

// ParseUserName trims s and checks it has 1–100 characters.
func ParseUserName(s string) (UserName, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > userNameMaxLen {
		return "", ErrInvalidName
	}
	return UserName(s), nil
}

// String returns the name.
func (n UserName) String() string {
	return string(n)
}
