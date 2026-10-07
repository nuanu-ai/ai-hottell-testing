import { useQuery } from '@tanstack/react-query';

import { meQueryOptions } from '../../shared/api';
import { PageHead, Panel, View } from '../../shared/ui';
import { ChangePasswordForm } from './ChangePasswordForm';
import { PasskeysCard } from './PasskeysCard';

export function ProfilePage() {
  // The shell lets in only a signed-in user, so the cache already holds them.
  const { data: me } = useQuery(meQueryOptions);

  return (
    <View>
      {me && <PageHead title={me.name} sub={me.email} />}
      <Panel
        title="Пароль"
        sub="Смена пароля закроет все остальные входы в аккаунт — этот останется"
      >
        <ChangePasswordForm />
      </Panel>
      <PasskeysCard />
    </View>
  );
}
