import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';

import type { components } from '../../shared/api';
import { formatAgo, formatDateTime, fullMoment } from '../../shared/lib/time';
import { Button, Card, Dialog, Empty, Notice, Table, Td, Th } from '../../shared/ui';
import { AddPasskeyForm } from './AddPasskeyForm';
import { passkeysQuery, useDeletePasskey } from './api/passkeys';

type Passkey = components['schemas']['PasskeyItem'];

// A moment in one of deploy's formats; the full moment is in the title.
function Moment({ at, format }: { at: string; format: (moment: Date) => string }) {
  const moment = new Date(at);
  return (
    <time dateTime={at} title={fullMoment(moment)}>
      {format(moment)}
    </time>
  );
}

// Holds the passkey as it was found: a refreshed list that drops the row keeps the dialog
// and its error in place.
function DeletePasskeyDialog({
  passkey,
  onClosed,
}: {
  passkey: Passkey | null;
  onClosed: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const remove = useDeletePasskey();
  const close = () => {
    dialogRef.current?.close();
  };

  useEffect(() => {
    if (passkey) dialogRef.current?.showModal();
  }, [passkey]);

  return (
    <Dialog
      ref={dialogRef}
      aria-label="Удалить passkey"
      onClose={() => {
        remove.reset();
        onClosed();
      }}
    >
      {passkey && (
        <div style={{ display: 'grid', gap: 14 }}>
          {remove.error && <Notice tone="err">{remove.error.message}</Notice>}
          <p style={{ margin: 0 }}>
            Удалить passkey «{passkey.name}»? Войти им больше не получится.
          </p>
          <div className="row" style={{ justifyContent: 'flex-end', marginTop: 4 }}>
            <Button onClick={close}>Отмена</Button>
            <Button
              className="плохо"
              disabled={remove.isPending}
              onClick={() => {
                remove.mutate(passkey.id, { onSuccess: close });
              }}
            >
              Удалить
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function PasskeysTable({ passkeys }: { passkeys: Passkey[] }) {
  const [deleting, setDeleting] = useState<Passkey | null>(null);

  // The dialog stays mounted when the last row goes, so it still closes and clears `deleting`.
  return (
    <>
      {passkeys.length === 0 ? (
        <Empty title="Passkey ещё нет">
          <p>Добавьте, чтобы входить без пароля</p>
        </Empty>
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>название</Th>
              <Th nowrap>добавлен</Th>
              <Th nowrap>последний вход</Th>
              <Th nowrap></Th>
            </tr>
          </thead>
          <tbody>
            {passkeys.map((passkey) => (
              <tr key={passkey.id}>
                <td>
                  <span className="cell-title">{passkey.name}</span>
                </td>
                <Td nowrap className="faint">
                  <Moment at={passkey.createdAt} format={formatDateTime} />
                </Td>
                <Td nowrap className="faint">
                  {passkey.lastUsedAt ? (
                    <Moment at={passkey.lastUsedAt} format={(moment) => formatAgo(moment)} />
                  ) : (
                    'ещё не использовался'
                  )}
                </Td>
                <Td nowrap>
                  <Button
                    size="sm"
                    onClick={() => {
                      setDeleting(passkey);
                    }}
                  >
                    Удалить
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <DeletePasskeyDialog
        passkey={deleting}
        onClosed={() => {
          setDeleting(null);
        }}
      />
    </>
  );
}

export function PasskeysCard() {
  const passkeys = useQuery(passkeysQuery);

  return (
    <Card>
      <h2>Passkey</h2>
      <p className="hint">
        Входите без пароля: отпечатком, лицом или ключом. Пароль при этом продолжает работать
      </p>
      {passkeys.isPending ? (
        <div className="loading">
          <span className="spinner" />
          Загружаем passkey…
        </div>
      ) : passkeys.isError ? (
        <Notice tone="err">
          {passkeys.error.message}{' '}
          <Button
            size="sm"
            disabled={passkeys.isFetching}
            onClick={() => {
              void passkeys.refetch();
            }}
          >
            Повторить
          </Button>
        </Notice>
      ) : (
        <PasskeysTable passkeys={passkeys.data.items} />
      )}
      <div style={{ marginTop: 16 }}>
        <AddPasskeyForm />
      </div>
    </Card>
  );
}
