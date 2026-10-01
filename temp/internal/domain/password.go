package domain

import "unicode/utf8"

const (
	passwordMinLen = 8
	passwordMaxLen = 128
)

// ValidatePassword checks that s has 8–128 characters; its composition is not restricted.
func ValidatePassword(s string) error {
	switch n := utf8.RuneCountInString(s); {
	case n < passwordMinLen:
		return ErrPasswordTooShort
	case n > passwordMaxLen:
		return ErrPasswordTooLong
	}
	return nil
}
