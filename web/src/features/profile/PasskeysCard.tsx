import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import type { components } from '../../shared/api';
import { formatAgo, formatDateTime } from '../../shared/lib/time';
import {
  Button,
  ConfirmDialog,
  Empty,
  LoadError,
  Loading,
  Moment,
  Panel,
  Stack,
  Table,
  Td,
  Th,
} from '../../shared/ui';
import type { ConfirmAction } from '../../shared/ui';
import { AddPasskeyForm } from './AddPasskeyForm';
import { passkeysQuery, useDeletePasskey } from './api/passkeys';

type Passkey = components['schemas']['PasskeyItem'];

function PasskeysTable({ passkeys }: { passkeys: Passkey[] }) {
  // Holds the passkey as it was found: a refreshed list that drops the row keeps the dialog
  // and its error in place.
  const [deleting, setDeleting] = useState<Passkey | null>(null);
  const remove = useDeletePasskey();
  const removeDeleting: ConfirmAction<void> = {
    error: remove.error,
    isPending: remove.isPending,
    reset: remove.reset,
    mutate: (_, options) => {
      if (deleting) remove.mutate(deleting.id, options);
    },
  };

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
              <Th>добавлен</Th>
              <Th>последний вход</Th>
              <Th></Th>
            </tr>
          </thead>
          <tbody>
            {passkeys.map((passkey) => (
              <tr key={passkey.id}>
                <Td wrap cellTitle>
                  {passkey.name}
                </Td>
                <Td className="muted">
                  <Moment at={passkey.createdAt} format={formatDateTime} />
                </Td>
                <Td className="muted">
                  {passkey.lastUsedAt ? (
                    <Moment at={passkey.lastUsedAt} format={(moment) => formatAgo(moment)} />
                  ) : (
                    'ещё не использовался'
                  )}
                </Td>
                <Td actions>
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
      <ConfirmDialog
        open={deleting !== null}
        title="Удалить passkey"
        text={`Удалить passkey «${deleting?.name ?? ''}»? Войти им больше не получится.`}
        confirm="Удалить"
        action={removeDeleting}
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
    <Panel
      title="Passkey"
      sub="Входите без пароля: отпечатком, лицом или ключом. Пароль при этом продолжает работать"
    >
      <Stack>
        {passkeys.isPending ? (
          <Loading>Загружаем passkey…</Loading>
        ) : passkeys.isError ? (
          <LoadError
            error={passkeys.error}
            retrying={passkeys.isFetching}
            onRetry={() => {
              void passkeys.refetch();
            }}
          />
        ) : (
          <PasskeysTable passkeys={passkeys.data.items} />
        )}
        <AddPasskeyForm />
      </Stack>
    </Panel>
  );
}
