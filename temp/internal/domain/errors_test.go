package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

func TestError_Error(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err         *domain.Error
		wantCode    string
		wantMessage string
	}{
		"invalid email":        {domain.ErrInvalidEmail, "invalid_email", "Проверьте email"},
		"invalid name":         {domain.ErrInvalidName, "invalid_name", "Укажите имя до 100 символов"},
		"password too short":   {domain.ErrPasswordTooShort, "password_too_short", "Пароль не короче 8 символов"},
		"password too long":    {domain.ErrPasswordTooLong, "password_too_long", "Пароль не длиннее 128 символов"},
		"invalid passkey name": {domain.ErrInvalidPasskeyName, "invalid_passkey_name", "Укажите название до 64 символов"},
		"wrong current password": {
			domain.ErrWrongCurrentPassword, "wrong_current_password", "Текущий пароль указан неверно",
		},
		"invalid credentials": {domain.ErrInvalidCredentials, "invalid_credentials", "Неверный email или пароль"},
		"unauthenticated":     {domain.ErrUnauthenticated, "unauthenticated", "Войдите, чтобы продолжить"},
		"link not found": {
			domain.ErrLinkNotFound, "link_not_found",
			"Ссылка не найдена. Проверьте, что скопировали её целиком, или попросите новую",
		},
		"link used":      {domain.ErrLinkUsed, "link_used", "Ссылка уже использована. Попросите новую"},
		"link expired":   {domain.ErrLinkExpired, "link_expired", "Срок действия ссылки истёк. Попросите выдать новую"},
		"user not found": {domain.ErrUserNotFound, "user_not_found", "Пользователь не найден"},
		"email taken":    {domain.ErrEmailTaken, "email_taken", "Пользователь с таким email уже есть"},
		"user not invited": {
			domain.ErrUserNotInvited, "user_not_invited", "Пользователь уже принял приглашение",
		},
		"user not active": {
			domain.ErrUserNotActive, "user_not_active", "Пользователь ещё не принял приглашение",
		},
		"cannot reset self": {domain.ErrCannotResetSelf, "cannot_reset_self", "Свой пароль меняйте в профиле"},
		"passkey not found": {domain.ErrPasskeyNotFound, "passkey_not_found", "Passkey не найден"},
		"passkey ceremony expired": {
			domain.ErrPasskeyCeremonyExpired, "passkey_ceremony_expired",
			"Время на подтверждение истекло. Попробуйте ещё раз",
		},
		"passkey verification failed": {
			domain.ErrPasskeyVerificationFailed, "passkey_verification_failed",
			"Не удалось подтвердить passkey. Попробуйте ещё раз",
		},
		"access key not found": {domain.ErrAccessKeyNotFound, "access_key_not_found", "Ключ не найден"},
		"access key revoked": {
			domain.ErrAccessKeyRevoked, "access_key_revoked", "Ключ отозван. Выпустите новый",
		},
		"settings version conflict": {
			domain.ErrSettingsVersionConflict, "settings_version_conflict",
			"Настройки уже изменили в другом месте. Обновите страницу и повторите",
		},
		"invalid telemetry settings": {
			domain.ErrInvalidTelemetrySettings, "invalid_telemetry_settings",
			"Настройки не прошли проверку. Обновите страницу и повторите",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.err.Code != tc.wantCode || tc.err.Message != tc.wantMessage {
				t.Fatalf("error = {%q, %q}, want {%q, %q}", tc.err.Code, tc.err.Message, tc.wantCode, tc.wantMessage)
			}
			if got := tc.err.Error(); got != tc.wantCode {
				t.Fatalf("Error() = %q, want %q", got, tc.wantCode)
			}
			wrapped := fmt.Errorf("context: %w", tc.err)
			var public *domain.Error
			if !errors.As(wrapped, &public) || public != tc.err {
				t.Fatalf("errors.As(wrapped) = %v, want %v", public, tc.err)
			}
		})
	}
}

func TestErrSessionNotFound(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("identify session: %w", domain.ErrSessionNotFound)

	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Fatal("errors.Is(err, ErrSessionNotFound) = false, want true")
	}
	var public *domain.Error
	if !errors.As(err, &public) || public != domain.ErrUnauthenticated {
		t.Fatalf("public error = %v, want %v", public, domain.ErrUnauthenticated)
	}
	if got := domain.ErrSessionNotFound.Error(); got != "session not found" {
		t.Fatalf("Error() = %q, want %q", got, "session not found")
	}
}

func TestErrCeremonyNotFound(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("take ceremony: %w", domain.ErrCeremonyNotFound)

	if !errors.Is(err, domain.ErrCeremonyNotFound) {
		t.Fatal("errors.Is(err, ErrCeremonyNotFound) = false, want true")
	}
	var public *domain.Error
	if !errors.As(err, &public) || public != domain.ErrPasskeyCeremonyExpired {
		t.Fatalf("public error = %v, want %v", public, domain.ErrPasskeyCeremonyExpired)
	}
	if got := domain.ErrCeremonyNotFound.Error(); got != "ceremony not found" {
		t.Fatalf("Error() = %q, want %q", got, "ceremony not found")
	}
}
