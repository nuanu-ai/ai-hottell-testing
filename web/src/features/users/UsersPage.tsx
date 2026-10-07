import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import type { components } from '../../shared/api';
import { plural } from '../../shared/lib/format';
import { formatAgo, formatDateTime } from '../../shared/lib/time';
import {
  Empty,
  LoadError,
  Loading,
  Moment,
  PageHead,
  Panel,
  Table,
  Tag,
  Td,
  Th,
  View,
} from '../../shared/ui';
import { usersQuery } from '../../shared/api';
import { InviteUser } from './InviteUser';
import { UserActionDialog, UserRowActions } from './UserActions';
import type { UserAction } from './UserActions';

type User = components['schemas']['UserListItem'];

// A moment in one of deploy's formats; the full moment is in the title.

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
    return (
      <Panel>
        <Empty title="Пользователей пока нет" />
      </Panel>
    );
  }
  return (
    <Panel>
      <Table>
        <thead>
          <tr>
            <Th>пользователь</Th>
            <Th>статус</Th>
            <Th>последний вход</Th>
            <Th>добавлен</Th>
            <Th></Th>
          </tr>
        </thead>
        <tbody>
          {users.map((user) => (
            <tr key={user.id}>
              <Td wrap>
                <span className="t">{user.name}</span>
                {user.isMe && (
                  <>
                    {' '}
                    <Tag tone="plain">это вы</Tag>
                  </>
                )}
                <div className="muted">{user.email}</div>
              </Td>
              <Td wrap>
                {user.status === 'active' ? (
                  <Tag tone="ok">активен</Tag>
                ) : (
                  <Tag tone="warn">приглашён</Tag>
                )}
              </Td>
              <Td className="muted">
                <LastLogin user={user} />
              </Td>
              <Td className="muted">
                <Moment at={user.createdAt} format={formatDateTime} />
              </Td>
              <Td actions>
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
    </Panel>
  );
}

// Only numbers from the loaded list: «N активны · M приглашены».
function usersSub(users: User[] | undefined): string {
  if (users === undefined) {
    return '—';
  }
  const active = users.filter((user) => user.status === 'active').length;
  const invited = users.length - active;
  return `${String(active)} ${plural(active, 'активен', 'активны', 'активны')} · ${String(invited)} ${plural(invited, 'приглашён', 'приглашены', 'приглашены')}`;
}

export function UsersPage() {
  const users = useQuery(usersQuery);

  return (
    <View>
      <PageHead title="Пользователи" sub={usersSub(users.data?.items)} action={<InviteUser />} />
      {users.isPending ? (
        <Loading>Загружаем пользователей…</Loading>
      ) : users.isError ? (
        <LoadError
          error={users.error}
          retrying={users.isFetching}
          onRetry={() => {
            void users.refetch();
          }}
        />
      ) : (
        <UsersTable users={users.data.items} />
      )}
    </View>
  );
}
