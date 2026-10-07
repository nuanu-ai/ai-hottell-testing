import { useState } from 'react';

import { ApiError } from '../../shared/api';
import { validateNewPassword, type PasswordError } from '../../shared/lib/password';
import { Button, Notice, PasswordInput, Stack } from '../../shared/ui';
import { useChangePassword } from './api/useChangePassword';

// The current password and a new one twice; the new one is sent only when
// validateNewPassword has nothing to say.
export function ChangePasswordForm() {
  const change = useChangePassword();
  const [current, setCurrent] = useState('');
  const [password, setPassword] = useState('');
  const [repeat, setRepeat] = useState('');
  const [fieldError, setFieldError] = useState<PasswordError | null>(null);
  const errorOf = (field: PasswordError['field']) =>
    fieldError?.field === field ? fieldError.message : undefined;

  // A wrong current password goes under its field; any other error is a notice above the form.
  const currentError =
    change.error instanceof ApiError && change.error.code === 'wrong_current_password'
      ? change.error.message
      : undefined;
  const formError = change.error && !currentError ? change.error.message : undefined;

  return (
    <Stack>
      {formError && <Notice tone="err">{formError}</Notice>}
      {change.isSuccess && <Notice tone="ok">Пароль изменён. Остальные входы закрыты</Notice>}
      <form
        className="narrow"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          const invalid = validateNewPassword(password, repeat);
          setFieldError(invalid);
          if (invalid) {
            change.reset();
            return;
          }
          change.mutate(
            { currentPassword: current, newPassword: password },
            {
              onSuccess: () => {
                setCurrent('');
                setPassword('');
                setRepeat('');
              },
            },
          );
        }}
      >
        <Stack>
          <PasswordInput
            label="Текущий пароль"
            autoComplete="current-password"
            value={current}
            error={currentError}
            onChange={(event) => {
              setCurrent(event.target.value);
            }}
          />
          <PasswordInput
            label="Новый пароль"
            autoComplete="new-password"
            maxLength={128}
            value={password}
            error={errorOf('password')}
            onChange={(event) => {
              setPassword(event.target.value);
            }}
          />
          <PasswordInput
            label="Повторите новый пароль"
            autoComplete="new-password"
            maxLength={128}
            value={repeat}
            error={errorOf('repeat')}
            onChange={(event) => {
              setRepeat(event.target.value);
            }}
          />
          <div>
            <Button variant="primary" type="submit" disabled={change.isPending}>
              Сменить пароль
            </Button>
          </div>
        </Stack>
      </form>
    </Stack>
  );
}
