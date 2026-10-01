import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { ApiError } from '../../shared/api';
import { Card, Empty, Notice } from '../../shared/ui';
import { passwordResetQuery, useCompletePasswordReset } from './api/queries';
import { SetPasswordForm } from './SetPasswordForm';

// 404 and 410: the link cannot be used, whatever happens next.
const isDeadLink = (error: Error) =>
  error instanceof ApiError && (error.status === 404 || error.status === 410);

// Laid out as the invite screen: one card in the middle, the holder named under the title.
export function ResetPage() {
  const { token } = useParams({ from: '/reset/$token' });
  const navigate = useNavigate();
  const reset = useQuery(passwordResetQuery(token));
  const complete = useCompletePasswordReset(token);

  return (
    <div className="вход-экран">
      <Card className="вход-карта">
        <h1 style={{ margin: '0 0 4px', fontSize: 20 }}>Телеметрия агентов</h1>
        {reset.isPending ? (
          <div className="loading">
            <span className="spinner" />
            Проверяем ссылку…
          </div>
        ) : reset.isError ? (
          isDeadLink(reset.error) ? (
            <Empty title={reset.error.message}>
              <Link to="/login" className="btn">
                Ко входу
              </Link>
            </Empty>
          ) : (
            <Notice tone="err">{reset.error.message}</Notice>
          )
        ) : (
          <>
            <p className="hint" style={{ margin: '0 0 20px' }}>
              Новый пароль для {reset.data.name} · {reset.data.email}
            </p>
            <SetPasswordForm
              submitLabel="Сохранить пароль и войти"
              pending={complete.isPending}
              error={complete.error?.message}
              onSubmit={(password) => {
                complete.mutate(password, {
                  onSuccess: () => {
                    void navigate({ to: '/users' });
                  },
                });
              }}
            />
            <p className="hint" style={{ margin: '16px 0 0' }}>
              После смены пароля все остальные входы в этот аккаунт закроются
            </p>
          </>
        )}
      </Card>
    </div>
  );
}
