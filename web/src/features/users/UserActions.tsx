import { useEffect, useRef } from 'react';
import type { ReactNode } from 'react';

import type { components } from '../../shared/api';
import { formatDateTime } from '../../shared/lib/time';
import { Actions, Button, CopyField, Dialog, Notice, Stack } from '../../shared/ui';
import { useIssuePasswordReset, useReissueInvite, useRevokeInvite } from './api/useUserActions';

type User = components['schemas']['UserListItem'];

export type UserAction = { kind: 'reissue' | 'revoke' | 'reset'; user: User };

const dialogNames: Record<UserAction['kind'], string> = {
  reissue: 'Новая ссылка',
  revoke: 'Отозвать приглашение',
  reset: 'Ссылка сброса пароля',
};

// The buttons of a row: an invited user's link can be reissued or revoked, an active one's
// password reset; your own row has none.
export function UserRowActions({
  user,
  onAction,
}: {
  user: User;
  onAction: (action: UserAction) => void;
}) {
  if (user.isMe) return null;
  const button = (kind: UserAction['kind'], label: string) => (
    <Button
      size="sm"
      onClick={() => {
        onAction({ kind, user });
      }}
    >
      {label}
    </Button>
  );
  if (user.status === 'invited') {
    return (
      <>
        {button('reissue', 'Новая ссылка')}
        {button('revoke', 'Отозвать')}
      </>
    );
  }
  return button('reset', 'Ссылка сброса пароля');
}

function Confirm({
  error,
  question,
  confirm,
  onCancel,
}: {
  error: Error | null;
  question: string;
  confirm: ReactNode;
  onCancel: () => void;
}) {
  return (
    <>
      <Stack>
        {error && <Notice tone="err">{error.message}</Notice>}
        <p>{question}</p>
      </Stack>
      <Actions justify="end" className="dialog-acts">
        <Button onClick={onCancel}>Отмена</Button>
        {confirm}
      </Actions>
    </>
  );
}

// A one-time link is shown once, right after it was issued.
function LinkResult({ url, note, onDone }: { url: string; note: string; onDone: () => void }) {
  return (
    <>
      <Stack>
        <CopyField value={url} />
        <p>{note}</p>
      </Stack>
      <Actions justify="end" className="dialog-acts">
        <Button variant="primary" onClick={onDone}>
          Готово
        </Button>
      </Actions>
    </>
  );
}

const validUntil = (expiresAt: string) =>
  `Действует до ${formatDateTime(new Date(expiresAt))}, сработает один раз.`;

function Reissue({ user, onClose }: { user: User; onClose: () => void }) {
  const reissue = useReissueInvite();
  if (reissue.data) {
    return (
      <LinkResult
        url={reissue.data.inviteUrl}
        note={validUntil(reissue.data.expiresAt)}
        onDone={onClose}
      />
    );
  }
  return (
    <Confirm
      error={reissue.error}
      question={`Старая ссылка для ${user.name} перестанет работать.`}
      onCancel={onClose}
      confirm={
        <Button
          variant="primary"
          disabled={reissue.isPending}
          onClick={() => {
            reissue.mutate(user.id);
          }}
        >
          Выпустить новую
        </Button>
      }
    />
  );
}

function Revoke({ user, onClose }: { user: User; onClose: () => void }) {
  const revoke = useRevokeInvite();
  return (
    <Confirm
      error={revoke.error}
      question={`Отозвать приглашение ${user.name} (${user.email})? Ссылка перестанет работать, пользователь исчезнет из списка.`}
      onCancel={onClose}
      confirm={
        <Button
          className="t-bad"
          disabled={revoke.isPending}
          onClick={() => {
            revoke.mutate(user.id, { onSuccess: onClose });
          }}
        >
          Отозвать
        </Button>
      }
    />
  );
}

function Reset({ user, onClose }: { user: User; onClose: () => void }) {
  const reset = useIssuePasswordReset();
  if (reset.data) {
    return (
      <LinkResult
        url={reset.data.resetUrl}
        note={`${validUntil(reset.data.expiresAt)} Прежняя неиспользованная ссылка сброса перестала работать.`}
        onDone={onClose}
      />
    );
  }
  return (
    <Confirm
      error={reset.error}
      question={`Выдать ${user.name} ссылку для нового пароля? Когда ей воспользуются, все входы ${user.name} закроются.`}
      onCancel={onClose}
      confirm={
        <Button
          variant="primary"
          disabled={reset.isPending}
          onClick={() => {
            reset.mutate(user.id);
          }}
        >
          Выдать ссылку
        </Button>
      }
    />
  );
}

const bodies = { reissue: Reissue, revoke: Revoke, reset: Reset };

// One dialog for the page, holding the user as the action found them: a refreshed list that
// changes or drops the row keeps the dialog and its error in place.
export function UserActionDialog({
  action,
  onClosed,
}: {
  action: UserAction | null;
  onClosed: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    if (action) dialogRef.current?.showModal();
  }, [action]);

  const Body = action && bodies[action.kind];
  return (
    <Dialog
      ref={dialogRef}
      title={action ? dialogNames[action.kind] : undefined}
      onClose={onClosed}
    >
      {action && Body && (
        <Body
          user={action.user}
          onClose={() => {
            dialogRef.current?.close();
          }}
        />
      )}
    </Dialog>
  );
}
