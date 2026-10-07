import { useNavigate, useSearch } from '@tanstack/react-router';
import { useRef, useState } from 'react';

import { isUnauthorized } from '../../shared/api';
import { isPasskeySupported, PasskeyCancelled, PasskeyFailed } from '../../shared/lib/webauthn';
import { Actions, Button, Field, Hint, Notice, Panel, Stack } from '../../shared/ui';
import { Fingerprint } from '../../shared/ui/icons';
import { nextPath } from './nextPath';
import { useLogin } from './useLogin';
import { usePasskeyLogin } from './usePasskeyLogin';

// Server errors keep their message; the browser's own ones get a short Russian line.
function passkeyErrorText(error: Error): string {
  if (error instanceof PasskeyCancelled) {
    return 'Вход по passkey отменён';
  }
  if (error instanceof PasskeyFailed) {
    return 'Не удалось войти по passkey. Попробуйте ещё раз';
  }
  return error.message;
}

// A server error reads in the err tone, as everywhere on the auth card; the browser's own
// cancel or failure is a warning.
function passkeyErrorTone(error: Error): 'err' | 'warn' {
  return error instanceof PasskeyCancelled || error instanceof PasskeyFailed ? 'warn' : 'err';
}

// One card in the middle of the screen, no theme switch.
export function LoginPage() {
  const { next } = useSearch({ from: '/login' });
  const navigate = useNavigate();
  const login = useLogin();
  const passkey = usePasskeyLogin();
  const busy = login.isPending || passkey.isPending;
  const passkeySupported = isPasskeySupported();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const passwordRef = useRef<HTMLInputElement>(null);

  return (
    <div className="auth-screen">
      <Panel className="auth-card">
        <Stack>
          <h1 className="brand auth-brand">
            <i />
            hottell
          </h1>
          <Hint>Вход для своих: доступ выдаёт приглашение</Hint>
          {login.error && (
            <Notice tone="err" role="alert">
              {login.error.message}
            </Notice>
          )}
          {passkey.error && (
            <Notice tone={passkeyErrorTone(passkey.error)} role="alert">
              {passkeyErrorText(passkey.error)}
            </Notice>
          )}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              passkey.reset();
              login.mutate(
                { email, password },
                {
                  onSuccess: () => {
                    void navigate({ href: nextPath(next) });
                  },
                  onError: (error) => {
                    if (isUnauthorized(error)) {
                      setPassword('');
                      passwordRef.current?.focus();
                    }
                  },
                },
              );
            }}
          >
            <Stack>
              <Field label="Email" htmlFor="login-email">
                <input
                  id="login-email"
                  type="email"
                  autoComplete="username"
                  autoFocus
                  value={email}
                  onChange={(event) => {
                    setEmail(event.target.value);
                  }}
                />
              </Field>
              <Field label="Пароль" htmlFor="login-password">
                <input
                  id="login-password"
                  ref={passwordRef}
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => {
                    setPassword(event.target.value);
                  }}
                />
              </Field>
              <Actions className="auth-actions">
                <Button variant="primary" type="submit" disabled={busy}>
                  {login.isPending ? 'Входим…' : 'Войти'}
                </Button>
                <button
                  type="button"
                  className={passkey.isPending ? 'btn passkey scanning' : 'btn passkey'}
                  title={
                    passkeySupported ? 'Войти по passkey' : 'Этот браузер не поддерживает passkey'
                  }
                  aria-label="Войти по passkey"
                  disabled={busy || !passkeySupported}
                  onClick={() => {
                    login.reset();
                    passkey.mutate(undefined, {
                      onSuccess: () => {
                        void navigate({ href: nextPath(next) });
                      },
                    });
                  }}
                >
                  <Fingerprint />
                </button>
              </Actions>
            </Stack>
          </form>
        </Stack>
      </Panel>
    </div>
  );
}
