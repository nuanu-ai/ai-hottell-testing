import { useState } from 'react';

import { ApiError } from '../../shared/api';
import {
  isPasskeySupported,
  PasskeyAlreadyRegistered,
  PasskeyCancelled,
  PasskeyFailed,
} from '../../shared/lib/webauthn';
import { Button, Notice, TextInput } from '../../shared/ui';
import { useAddPasskey } from './api/passkeys';

// The browser's own refusals are warnings with a short Russian line; the server's errors keep
// their message.
function addError(error: Error): { tone: 'warn' | 'err'; text: string } {
  if (error instanceof PasskeyCancelled) return { tone: 'warn', text: 'Добавление отменено' };
  if (error instanceof PasskeyAlreadyRegistered) {
    return { tone: 'warn', text: 'Этот passkey уже добавлен' };
  }
  if (error instanceof PasskeyFailed) {
    return { tone: 'err', text: 'Не удалось добавить passkey. Попробуйте ещё раз' };
  }
  return { tone: 'err', text: error.message };
}

// A name and the button in one row; a browser without WebAuthn gets a notice instead.
export function AddPasskeyForm() {
  const add = useAddPasskey();
  const [name, setName] = useState('');
  const [emptyName, setEmptyName] = useState(false);

  if (!isPasskeySupported()) {
    return <Notice tone="info">Этот браузер не поддерживает passkey</Notice>;
  }

  const nameRejected =
    add.error instanceof ApiError && add.error.code === 'invalid_passkey_name'
      ? add.error.message
      : undefined;
  const notice = add.error && !nameRejected ? addError(add.error) : undefined;

  return (
    <>
      {notice && (
        <Notice tone={notice.tone} style={{ marginBottom: 12 }}>
          {notice.text}
        </Notice>
      )}
      {add.data && (
        <Notice tone="ok" style={{ marginBottom: 12 }}>
          Passkey «{add.data.name}» добавлен
        </Notice>
      )}
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          const trimmed = name.trim();
          setEmptyName(trimmed === '');
          if (trimmed === '') {
            add.reset();
            return;
          }
          add.mutate(trimmed, {
            onSuccess: () => {
              setName('');
            },
          });
        }}
      >
        {/* Aligned to the top, so an error under the field does not move the button. */}
        <div className="row" style={{ alignItems: 'flex-start' }}>
          <div style={{ flex: '0 1 380px' }}>
            <TextInput
              label="Название"
              id="passkey-name"
              placeholder="Например, MacBook или iPhone"
              maxLength={64}
              disabled={add.isPending}
              value={name}
              error={emptyName ? 'Введите название' : nameRejected}
              onChange={(event) => {
                setName(event.target.value);
              }}
            />
          </div>
          {/* An empty label of the same height puts the button level with the input. */}
          <div className="field">
            <label aria-hidden="true">&nbsp;</label>
            <Button variant="primary" type="submit" disabled={add.isPending}>
              {add.isPending ? 'Ждём подтверждения…' : 'Добавить passkey'}
            </Button>
          </div>
        </div>
      </form>
    </>
  );
}
