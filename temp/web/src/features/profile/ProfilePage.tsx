import { useQuery } from '@tanstack/react-query';

import { meQueryOptions } from '../../shared/api';
import { Card, PageTitle } from '../../shared/ui';
import { ChangePasswordForm } from './ChangePasswordForm';
import { PasskeysCard } from './PasskeysCard';

export function ProfilePage() {
  // The shell lets in only a signed-in user, so the cache already holds them.
  const { data: me } = useQuery(meQueryOptions);

  return (
    <>
      {me && (
        <div style={{ marginBottom: 16 }}>
          <PageTitle>{me.name}</PageTitle>
          <div className="faint" style={{ fontSize: 13 }}>
            {me.email}
          </div>
        </div>
      )}
      <Card>
        <h2>Пароль</h2>
        <p className="hint">Смена пароля закроет все остальные входы в аккаунт — этот останется</p>
        <ChangePasswordForm />
      </Card>
      <PasskeysCard />
    </>
  );
}
