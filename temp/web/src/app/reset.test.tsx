import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import { handlers, me, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

const token = 'reset-token';
const holder = { email: 'boris@example.test', name: 'Борис Иванов' };
const linkNotFound = {
  code: 'link_not_found',
  message: 'Ссылка не найдена. Проверьте, что скопировали её целиком, или попросите новую',
};
const linkUsed = { code: 'link_used', message: 'Ссылка уже использована. Попросите новую' };

function renderReset() {
  const app = createApp(createMemoryHistory({ initialEntries: [`/reset/${token}`] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

async function submit(password: string) {
  fireEvent.change(await screen.findByLabelText('Пароль'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Повторите пароль'), { target: { value: password } });
  act(() => {
    screen.getByRole('button', { name: 'Сохранить пароль и войти' }).click();
  });
}

beforeEach(() => {
  server.use(handlers.getMeError(401), handlers.getPasswordReset(holder));
});

it('shows the loading state until the link is checked', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.get(
      `/api/password-resets/${token}`,
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderReset();

  const loading = await screen.findByText('Проверяем ссылку…');
  expect(loading).toHaveClass('loading');
  expect(loading.querySelector('.spinner')).not.toBeNull();
  act(() => {
    answer(HttpResponse.json(holder));
  });
  expect(await screen.findByLabelText('Пароль')).toBeInTheDocument();
});

it('on a valid link shows whose password it sets, the form and the warning', async () => {
  renderReset();

  expect(await screen.findByText(`Новый пароль для ${holder.name} · ${holder.email}`)).toHaveClass(
    'hint',
  );
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Телеметрия агентов');
  expect(document.querySelector('div.вход-экран > div.card.вход-карта')).not.toBeNull();
  for (const label of ['Пароль', 'Повторите пароль']) {
    const input = screen.getByLabelText(label);
    expect(input).toHaveAttribute('type', 'password');
    expect(input).toHaveAttribute('autocomplete', 'new-password');
  }
  const button = screen.getByRole('button', { name: 'Сохранить пароль и войти' });
  expect(button).toHaveClass('btn', 'btn-primary');
  const warning = screen.getByText(
    'После смены пароля все остальные входы в этот аккаунт закроются',
  );
  expect(warning).toHaveClass('hint');
  expect(button.compareDocumentPosition(warning) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

it.each([
  [404, linkNotFound],
  [410, linkUsed],
])('on %i shows the server message and a way to sign in', async (status, body) => {
  server.use(handlers.getPasswordResetError(status, body));
  renderReset();

  const empty = (await screen.findByText(body.message)).closest('.empty');
  expect(empty).not.toBeNull();
  const toLogin = screen.getByRole('link', { name: 'Ко входу' });
  expect(toLogin).toHaveClass('btn');
  expect(toLogin).toHaveAttribute('href', '/login');
  expect(screen.queryByLabelText('Пароль')).toBeNull();
});

it('shows a server error above the form', async () => {
  server.use(handlers.completePasswordResetError(410, linkUsed));
  renderReset();

  await submit('длинный пароль');

  const notice = await screen.findByRole('alert');
  expect(notice).toHaveTextContent(linkUsed.message);
  expect(screen.getByRole('button', { name: 'Сохранить пароль и войти' })).toBeEnabled();
});

it('on success sets the password, drops the cache and opens /users', async () => {
  let sent: unknown;
  server.use(
    http.post(`/api/password-resets/${token}/complete`, async ({ request }) => {
      sent = await request.json();
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderReset();
  await screen.findByLabelText('Пароль');
  const clear = vi.spyOn(app.queryClient, 'clear');

  await submit('длинный пароль');

  expect(await screen.findByText('Пользователи', { selector: 'span.крупно' })).toBeInTheDocument();
  expect(app.router.state.location.href).toBe('/users');
  expect(sent).toEqual({ password: 'длинный пароль' });
  expect(clear).toHaveBeenCalledOnce();
  expect(await screen.findByTitle(`${me.name} — выйти`)).toBeInTheDocument();
});
