import { useNavigate, useSearch } from '@tanstack/react-router';
import { useRef, useState } from 'react';

import { isUnauthorized } from '../../shared/api';
import { isPasskeySupported, PasskeyCancelled, PasskeyFailed } from '../../shared/lib/webauthn';
import { Button, Card, Field, Notice } from '../../shared/ui';
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

// Markup of deploy's login.html: one card in the middle of the screen, no theme switch.
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
    <div className="вход-экран">
      <Card className="вход-карта">
        <h1 style={{ margin: '0 0 4px', fontSize: 20 }}>Телеметрия агентов</h1>
        <p className="hint" style={{ margin: '0 0 20px' }}>
          Вход для своих: доступ выдаёт приглашение
        </p>
        {login.error && (
          <Notice tone="warn" role="alert" style={{ marginBottom: 12 }}>
            {login.error.message}
          </Notice>
        )}
        {passkey.error && (
          <Notice tone="warn" role="alert" style={{ marginBottom: 12 }}>
            {passkeyErrorText(passkey.error)}
          </Notice>
        )}
        <form
          style={{ display: 'grid', gap: 14 }}
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
          <div className="login-actions">
            <Button variant="primary" type="submit" disabled={busy}>
              {login.isPending ? 'Входим…' : 'Войти'}
            </Button>
            <button
              type="button"
              className={passkey.isPending ? 'btn-passkey scanning' : 'btn-passkey'}
              title={passkeySupported ? 'Войти по passkey' : 'Этот браузер не поддерживает passkey'}
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
          </div>
        </form>
      </Card>
    </div>
  );
}
