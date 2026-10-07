import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';

import { ApiError } from '../../shared/api';
import { Empty, Hint, Loading, Notice, Panel, Stack } from '../../shared/ui';
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
    <div className="auth-screen">
      <Panel className="auth-card">
        <Stack>
          <h1 className="brand auth-brand">
            <i />
            hottell
          </h1>
          {reset.isPending ? (
            <Loading>Проверяем ссылку…</Loading>
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
              <Hint>
                Новый пароль для {reset.data.name} · {reset.data.email}
              </Hint>
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
              <Hint>После смены пароля все остальные входы в этот аккаунт закроются</Hint>
            </>
          )}
        </Stack>
      </Panel>
    </div>
  );
}
