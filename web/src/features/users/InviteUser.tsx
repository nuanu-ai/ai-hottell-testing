import { useRef, useState } from 'react';

import { ApiError } from '../../shared/api';
import type { components } from '../../shared/api';
import { formatDateTime } from '../../shared/lib/time';
import {
  Actions,
  Button,
  CopyField,
  Dialog,
  EmailInput,
  Notice,
  Stack,
  TextInput,
} from '../../shared/ui';
import { useInviteUser } from './api/useInviteUser';

type Invitation = components['schemas']['InviteUserResponse'];

// The link is shown once: the system sends no mail, the inviter passes it on.
function InviteResult({ invitation, onDone }: { invitation: Invitation; onDone: () => void }) {
  return (
    <>
      <Stack>
        <p>
          Ссылка для {invitation.user.name}. Она действует до{' '}
          {formatDateTime(new Date(invitation.expiresAt))} и сработает один раз. Передайте её сами —
          писем система не отправляет.
        </p>
        <CopyField value={invitation.inviteUrl} />
      </Stack>
      <Actions justify="end" className="dialog-acts">
        <Button variant="primary" onClick={onDone}>
          Готово
        </Button>
      </Actions>
    </>
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
      onSubmit={(event) => {
        event.preventDefault();
        invite.mutate({ name, email });
      }}
    >
      <Stack>
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
      </Stack>
      <Actions justify="end" className="dialog-acts">
        <Button onClick={onClose}>Отмена</Button>
        <Button variant="primary" type="submit" disabled={invite.isPending}>
          Создать ссылку
        </Button>
      </Actions>
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
        title="Пригласить пользователя"
        onClose={() => {
          setRound((current) => current + 1);
        }}
      >
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
