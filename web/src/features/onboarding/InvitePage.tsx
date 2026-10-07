import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { ApiError } from '../../shared/api';
import { Empty, Hint, Loading, Notice, Panel, Stack } from '../../shared/ui';
import { inviteQuery, useAcceptInvite } from './api/queries';
import { SetPasswordForm } from './SetPasswordForm';

// 404 and 410: the link cannot be used, whatever happens next.
const isDeadLink = (error: Error) =>
  error instanceof ApiError && (error.status === 404 || error.status === 410);

// One card in the middle of the screen, the holder named under the title.
export function InvitePage() {
  const { token } = useParams({ from: '/invite/$token' });
  const navigate = useNavigate();
  const invite = useQuery(inviteQuery(token));
  const accept = useAcceptInvite(token);

  return (
    <div className="auth-screen">
      <Panel className="auth-card">
        <Stack>
          <h1 className="brand auth-brand">
            <i />
            hottell
          </h1>
          {invite.isPending ? (
            <Loading>Проверяем приглашение…</Loading>
          ) : invite.isError ? (
            isDeadLink(invite.error) ? (
              <Empty title={invite.error.message}>
                <Link to="/login" className="btn">
                  Ко входу
                </Link>
              </Empty>
            ) : (
              <Notice tone="err">{invite.error.message}</Notice>
            )
          ) : (
            <>
              <Hint>
                Приглашение для {invite.data.name} · {invite.data.email}
              </Hint>
              <SetPasswordForm
                submitLabel="Задать пароль и войти"
                pending={accept.isPending}
                error={accept.error?.message}
                onSubmit={(password) => {
                  accept.mutate(password, {
                    onSuccess: () => {
                      void navigate({ to: '/users' });
                    },
                  });
                }}
              />
            </>
          )}
        </Stack>
      </Panel>
    </div>
  );
}
