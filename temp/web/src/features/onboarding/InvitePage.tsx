import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { ApiError } from '../../shared/api';
import { Card, Empty, Notice } from '../../shared/ui';
import { inviteQuery, useAcceptInvite } from './api/queries';
import { SetPasswordForm } from './SetPasswordForm';

// 404 and 410: the link cannot be used, whatever happens next.
const isDeadLink = (error: Error) =>
  error instanceof ApiError && (error.status === 404 || error.status === 410);

// Markup of the sign-in screen: one card in the middle, the holder named under the title.
export function InvitePage() {
  const { token } = useParams({ from: '/invite/$token' });
  const navigate = useNavigate();
  const invite = useQuery(inviteQuery(token));
  const accept = useAcceptInvite(token);

  return (
    <div className="вход-экран">
      <Card className="вход-карта">
        <h1 style={{ margin: '0 0 4px', fontSize: 20 }}>Телеметрия агентов</h1>
        {invite.isPending ? (
          <div className="loading">
            <span className="spinner" />
            Проверяем приглашение…
          </div>
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
            <p className="hint" style={{ margin: '0 0 20px' }}>
              Приглашение для {invite.data.name} · {invite.data.email}
            </p>
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
      </Card>
    </div>
  );
}
