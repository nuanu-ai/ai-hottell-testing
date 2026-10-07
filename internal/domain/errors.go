package domain

// Error is a domain error that reaches API clients as is: Code is the machine-readable
// code of the API Error body, Message the Russian text fit to show a person.
type Error struct {
	Code    string
	Message string
}

// internalError is a domain error that never reaches API clients as is: it unwraps to
// the public error that is answered in its place.
type internalError struct {
	text   string
	public *Error
}

// Input errors.
var (
	ErrInvalidEmail         = &Error{Code: "invalid_email", Message: "Проверьте email"}
	ErrInvalidName          = &Error{Code: "invalid_name", Message: "Укажите имя до 100 символов"}
	ErrPasswordTooShort     = &Error{Code: "password_too_short", Message: "Пароль не короче 8 символов"}
	ErrPasswordTooLong      = &Error{Code: "password_too_long", Message: "Пароль не длиннее 128 символов"}
	ErrInvalidPasskeyName   = &Error{Code: "invalid_passkey_name", Message: "Укажите название до 64 символов"}
	ErrWrongCurrentPassword = &Error{Code: "wrong_current_password", Message: "Текущий пароль указан неверно"}
)

// Sign-in errors.
var (
	ErrInvalidCredentials = &Error{Code: "invalid_credentials", Message: "Неверный email или пароль"}
	ErrUnauthenticated    = &Error{Code: "unauthenticated", Message: "Войдите, чтобы продолжить"}
)

// Link errors.
var (
	ErrLinkNotFound = &Error{
		Code:    "link_not_found",
		Message: "Ссылка не найдена. Проверьте, что скопировали её целиком, или попросите новую",
	}
	ErrLinkUsed    = &Error{Code: "link_used", Message: "Ссылка уже использована. Попросите новую"}
	ErrLinkExpired = &Error{Code: "link_expired", Message: "Срок действия ссылки истёк. Попросите выдать новую"}
)

// User errors.
var (
	ErrUserNotFound    = &Error{Code: "user_not_found", Message: "Пользователь не найден"}
	ErrEmailTaken      = &Error{Code: "email_taken", Message: "Пользователь с таким email уже есть"}
	ErrUserNotInvited  = &Error{Code: "user_not_invited", Message: "Пользователь уже принял приглашение"}
	ErrUserNotActive   = &Error{Code: "user_not_active", Message: "Пользователь ещё не принял приглашение"}
	ErrCannotResetSelf = &Error{Code: "cannot_reset_self", Message: "Свой пароль меняйте в профиле"}
)

// Passkey errors.
var (
	ErrPasskeyNotFound        = &Error{Code: "passkey_not_found", Message: "Passkey не найден"}
	ErrPasskeyCeremonyExpired = &Error{
		Code:    "passkey_ceremony_expired",
		Message: "Время на подтверждение истекло. Попробуйте ещё раз",
	}
	ErrPasskeyVerificationFailed = &Error{
		Code:    "passkey_verification_failed",
		Message: "Не удалось подтвердить passkey. Попробуйте ещё раз",
	}
)

// Access key errors.
var (
	ErrAccessKeyNotFound = &Error{Code: "access_key_not_found", Message: "Ключ не найден"}
	ErrAccessKeyRevoked  = &Error{Code: "access_key_revoked", Message: "Ключ отозван. Выпустите новый"}
)

// Settings errors.
var (
	ErrInvalidTelemetrySettings = &Error{
		Code:    "invalid_telemetry_settings",
		Message: "Настройки не прошли проверку. Обновите страницу и повторите",
	}
	ErrSettingsVersionConflict = &Error{
		Code:    "settings_version_conflict",
		Message: "Настройки уже изменили в другом месте. Обновите страницу и повторите",
	}
)

// Analytics errors.
var (
	// ErrInvalidAnalyticsFilter: a parameter of the analytics filter is out of range; an
	// *InvalidFilterError names it.
	ErrInvalidAnalyticsFilter = &Error{Code: "invalid_filter", Message: "Фильтр аналитики задан неверно"}
	// ErrAnalyticsUnavailable: the server has no telemetry store to build the analytics from.
	ErrAnalyticsUnavailable = &Error{Code: "analytics_unavailable", Message: "Аналитика недоступна: хранилище телеметрии не подключено"}
	// ErrInvalidTopicKey:a hidden topic's key is empty, longer than 200 characters or not text.
	ErrInvalidTopicKey = &Error{Code: "invalid_topic_key", Message: "Ключ темы задан неверно"}
	// ErrAnalyticsSessionNotFound: no session of the id matches the agent and the person.
	ErrAnalyticsSessionNotFound = &Error{Code: "not_found", Message: "Сессия не найдена"}
	// ErrAnalyticsSessionAmbiguous: several sessions share the id; the agent and the person tell
	// them apart.
	ErrAnalyticsSessionAmbiguous = &Error{
		Code:    "ambiguous_session",
		Message: "Сессий с таким id несколько. Укажите агента и пользователя",
	}
)

// InvalidFilterError names the parameter of the analytics filter that is out of range and the
// values it takes. It unwraps to ErrInvalidAnalyticsFilter.
type InvalidFilterError struct {
	Field  string
	Reason string
}

func (e *InvalidFilterError) Error() string {
	return "invalid analytics filter: " + e.Field + ": " + e.Reason
}

// Message is the text for a person: the parameter and the values it takes.
func (e *InvalidFilterError) Message() string {
	return e.Field + ": " + e.Reason
}

// Unwrap returns ErrInvalidAnalyticsFilter.
func (e *InvalidFilterError) Unwrap() error {
	return ErrInvalidAnalyticsFilter
}

// Internal errors.
var (
	// ErrSessionNotFound: no live session matches the token; clients get ErrUnauthenticated.
	ErrSessionNotFound error = &internalError{text: "session not found", public: ErrUnauthenticated}
	// ErrCeremonyNotFound: no live WebAuthn ceremony of the expected kind matches;
	// clients get ErrPasskeyCeremonyExpired.
	ErrCeremonyNotFound error = &internalError{text: "ceremony not found", public: ErrPasskeyCeremonyExpired}
)

// Error returns the code, so the error text stays static and groupable.
func (e *Error) Error() string {
	return e.Code
}

func (e *internalError) Error() string {
	return e.text
}

// Unwrap returns the public error answered in place of e.
func (e *internalError) Unwrap() error {
	return e.public
}
