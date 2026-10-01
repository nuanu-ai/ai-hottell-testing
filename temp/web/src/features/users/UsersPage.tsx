import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import type { components } from '../../shared/api';
import { formatAgo, formatDateTime, fullMoment } from '../../shared/lib/time';
import { Badge, Button, Empty, Notice, PageTitle, Table, Td, Th } from '../../shared/ui';
import { usersQuery } from './api/queries';
import { InviteUser } from './InviteUser';
import { UserActionDialog, UserRowActions } from './UserActions';
import type { UserAction } from './UserActions';

type User = components['schemas']['UserListItem'];

// A moment in one of deploy's formats; the full moment is in the title.
function Moment({ at, format }: { at: string; format: (moment: Date) => string }) {
  const moment = new Date(at);
  return (
    <time dateTime={at} title={fullMoment(moment)}>
      {format(moment)}
    </time>
  );
}

function LastLogin({ user }: { user: User }) {
  if (user.status === 'invited' && user.inviteExpiresAt) {
    return (
      <>
        приглашение до <Moment at={user.inviteExpiresAt} format={formatDateTime} />
      </>
    );
  }
  if (user.lastLoginAt) {
    return <Moment at={user.lastLoginAt} format={(moment) => formatAgo(moment)} />;
  }
  return <>—</>;
}

function UsersTable({ users }: { users: User[] }) {
  const [action, setAction] = useState<UserAction | null>(null);

  if (users.length === 0) {
    return <Empty title="Пользователей пока нет" />;
  }
  return (
    <>
      <Table>
        <thead>
          <tr>
            <Th>пользователь</Th>
            <Th>статус</Th>
            <Th nowrap>последний вход</Th>
            <Th nowrap>добавлен</Th>
            <Th nowrap></Th>
          </tr>
        </thead>
        <tbody>
          {users.map((user) => (
            <tr key={user.id}>
              <td>
                <span className="cell-title">{user.name}</span>
                {user.isMe && (
                  <>
                    {' '}
                    <Badge tone="quiet">это вы</Badge>
                  </>
                )}
                <div className="faint" style={{ fontSize: 12.5 }}>
                  {user.email}
                </div>
              </td>
              <td>
                {user.status === 'active' ? (
                  <Badge tone="ok">активен</Badge>
                ) : (
                  <Badge tone="warn">приглашён</Badge>
                )}
              </td>
              <Td nowrap className="faint">
                <LastLogin user={user} />
              </Td>
              <Td nowrap className="faint">
                <Moment at={user.createdAt} format={formatDateTime} />
              </Td>
              <Td nowrap>
                <UserRowActions user={user} onAction={setAction} />
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
      <UserActionDialog
        action={action}
        onClosed={() => {
          setAction(null);
        }}
      />
    </>
  );
}

export function UsersPage() {
  const users = useQuery(usersQuery);

  return (
    <>
      <div className="row" style={{ justifyContent: 'space-between', marginBottom: 16 }}>
        <PageTitle>Пользователи</PageTitle>
        <InviteUser />
      </div>
      {users.isPending ? (
        <div className="loading">
          <span className="spinner" />
          Загружаем пользователей…
        </div>
      ) : users.isError ? (
        <Notice tone="err">
          {users.error.message}{' '}
          <Button
            size="sm"
            disabled={users.isFetching}
            onClick={() => {
              void users.refetch();
            }}
          >
            Повторить
          </Button>
        </Notice>
      ) : (
        <UsersTable users={users.data.items} />
      )}
    </>
  );
}
