import { useRef, useState } from 'react';

import { ApiError } from '../../shared/api';
import type { components } from '../../shared/api';
import { formatDateTime } from '../../shared/lib/time';
import { Button, CopyField, Dialog, EmailInput, Notice, TextInput } from '../../shared/ui';
import { useInviteUser } from './api/useInviteUser';

type Invitation = components['schemas']['InviteUserResponse'];

const actionsStyle = { justifyContent: 'flex-end', marginTop: 4 } as const;

// The link is shown once: the system sends no mail, the inviter passes it on.
function InviteResult({ invitation, onDone }: { invitation: Invitation; onDone: () => void }) {
  return (
    <div style={{ display: 'grid', gap: 14 }}>
      <p style={{ margin: 0 }}>
        Ссылка для {invitation.user.name}. Она действует до{' '}
        {formatDateTime(new Date(invitation.expiresAt))} и сработает один раз. Передайте её сами —
        писем система не отправляет.
      </p>
      <CopyField value={invitation.inviteUrl} />
      <div className="row" style={actionsStyle}>
        <Button variant="primary" onClick={onDone}>
          Готово
        </Button>
      </div>
    </div>
  );
}

function InviteForm({ onClose }: { onClose: () => void }) {
  const invite = useInviteUser();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');

  if (invite.data) {
    return <InviteResult invitation={invite.data} onDone={onClose} />;
  }

  // Errors about a field go under it; any other one is a notice above the form.
  const code = invite.error instanceof ApiError ? invite.error.code : undefined;
  const nameError = code === 'invalid_name' ? invite.error?.message : undefined;
  const emailError =
    code === 'invalid_email' || code === 'email_taken' ? invite.error?.message : undefined;
  const formError = invite.error && !nameError && !emailError ? invite.error.message : undefined;

  return (
    <form
      style={{ display: 'grid', gap: 14 }}
      onSubmit={(event) => {
        event.preventDefault();
        invite.mutate({ name, email });
      }}
    >
      {formError && <Notice tone="err">{formError}</Notice>}
      <TextInput
        label="Имя"
        id="invite-name"
        required
        maxLength={100}
        autoFocus
        value={name}
        error={nameError}
        onChange={(event) => {
          setName(event.target.value);
        }}
      />
      <EmailInput
        label="Email"
        id="invite-email"
        required
        value={email}
        error={emailError}
        onChange={(event) => {
          setEmail(event.target.value);
        }}
      />
      <div className="row" style={actionsStyle}>
        <Button onClick={onClose}>Отмена</Button>
        <Button variant="primary" type="submit" disabled={invite.isPending}>
          Создать ссылку
        </Button>
      </div>
    </form>
  );
}

// The «Пригласить» button and its dialog; closing the dialog starts the next invite afresh.
export function InviteUser() {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [round, setRound] = useState(0);

  return (
    <>
      <Button
        variant="primary"
        onClick={() => {
          dialogRef.current?.showModal();
        }}
      >
        Пригласить
      </Button>
      <Dialog
        ref={dialogRef}
        aria-labelledby="invite-title"
        onClose={() => {
          setRound((current) => current + 1);
        }}
      >
        <h3 id="invite-title" style={{ margin: '0 0 16px' }}>
          Пригласить пользователя
        </h3>
        <InviteForm
          key={round}
          onClose={() => {
            dialogRef.current?.close();
          }}
        />
      </Dialog>
    </>
  );
}
