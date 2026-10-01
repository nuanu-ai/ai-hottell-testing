import { useState } from 'react';

import { validateNewPassword, type PasswordError } from '../../shared/lib/password';
import { Button, Notice, PasswordInput } from '../../shared/ui';

type SetPasswordFormProps = {
  submitLabel: string;
  pending: boolean;
  /** The server's message, shown above the form. */
  error?: string;
  onSubmit: (password: string) => void;
};

// A new password twice; sent only when validateNewPassword has nothing to say.
export function SetPasswordForm({ submitLabel, pending, error, onSubmit }: SetPasswordFormProps) {
  const [password, setPassword] = useState('');
  const [repeat, setRepeat] = useState('');
  const [fieldError, setFieldError] = useState<PasswordError | null>(null);
  const errorOf = (field: PasswordError['field']) =>
    fieldError?.field === field ? fieldError.message : undefined;

  return (
    <>
      {error && (
        <Notice tone="warn" role="alert" style={{ marginBottom: 12 }}>
          {error}
        </Notice>
      )}
      <form
        style={{ display: 'grid', gap: 14 }}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          const invalid = validateNewPassword(password, repeat);
          setFieldError(invalid);
          if (!invalid) {
            onSubmit(password);
          }
        }}
      >
        <PasswordInput
          label="Пароль"
          autoComplete="new-password"
          maxLength={128}
          autoFocus
          value={password}
          error={errorOf('password')}
          onChange={(event) => {
            setPassword(event.target.value);
          }}
        />
        <PasswordInput
          label="Повторите пароль"
          autoComplete="new-password"
          maxLength={128}
          value={repeat}
          error={errorOf('repeat')}
          onChange={(event) => {
            setRepeat(event.target.value);
          }}
        />
        <div className="login-actions">
          <Button variant="primary" type="submit" disabled={pending}>
            {submitLabel}
          </Button>
        </div>
      </form>
    </>
  );
}
