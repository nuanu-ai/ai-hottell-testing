// Package middleware holds the HTTP middleware of the API: the origin check against
// CSRF, the session cookie and the check that an operation has a session.
package middleware

import (
	"net/http"
	"time"
)

// SessionCookieName is the cookie that carries the session token.
const SessionCookieName = "ht_session"

// CeremonyCookieName is the cookie that ties a WebAuthn ceremony to the browser that
// began it.
const CeremonyCookieName = "ht_webauthn"

// ceremonyMaxAge is the Max-Age of the ceremony cookie in seconds: a ceremony lives
// five minutes.
const ceremonyMaxAge = 300

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}

// Cookies sets and clears the session cookie (HttpOnly, SameSite=Lax, Path=/) and the
// ceremony cookie (HttpOnly, SameSite=Strict, Path=/api).
type Cookies struct {
	secure bool
	clock  Clock
}

// NewCookies returns Cookies that mark the cookie Secure when secure is set and count
// its Max-Age from clock.
func NewCookies(secure bool, clock Clock) *Cookies {
	return &Cookies{secure: secure, clock: clock}
}

// SetSessionCookie sets the session cookie to token until expiresAt.
func (c *Cookies) SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, c.SessionCookie(token, expiresAt))
}

// ClearSessionCookie tells the browser to delete the session cookie.
func (c *Cookies) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, c.ExpiredSessionCookie())
}

// SessionCookie returns the session cookie carrying token until expiresAt.
func (c *Cookies) SessionCookie(token string, expiresAt time.Time) *http.Cookie {
	maxAge := int(expiresAt.Sub(c.clock.Now()).Round(time.Second) / time.Second)
	// Max-Age 0 would drop the attribute and turn the cookie into a browser-session one.
	return c.cookie(token, max(maxAge, 1))
}

// ExpiredSessionCookie returns the session cookie that tells the browser to delete it.
func (c *Cookies) ExpiredSessionCookie() *http.Cookie {
	// A negative MaxAge is sent as Max-Age=0.
	return c.cookie("", -1)
}

func (c *Cookies) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// CeremonyCookie returns the ceremony cookie carrying ceremonyID for five minutes.
func (c *Cookies) CeremonyCookie(ceremonyID string) *http.Cookie {
	return c.ceremonyCookie(ceremonyID, ceremonyMaxAge)
}

// ExpiredCeremonyCookie returns the ceremony cookie that tells the browser to delete it.
func (c *Cookies) ExpiredCeremonyCookie() *http.Cookie {
	return c.ceremonyCookie("", -1)
}

func (c *Cookies) ceremonyCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CeremonyCookieName,
		Value:    value,
		Path:     "/api",
		MaxAge:   maxAge,
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}
