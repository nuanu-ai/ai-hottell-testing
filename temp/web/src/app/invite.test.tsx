import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import { handlers, me, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

const token = 'invite-token';
const holder = { email: 'boris@example.test', name: 'Борис Иванов' };
const linkNotFound = {
  code: 'link_not_found',
  message: 'Ссылка не найдена. Проверьте, что скопировали её целиком, или попросите новую',
};
const linkUsed = { code: 'link_used', message: 'Ссылка уже использована. Попросите новую' };

function renderInvite() {
  const app = createApp(createMemoryHistory({ initialEntries: [`/invite/${token}`] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

async function submit(password: string, repeat = password) {
  fireEvent.change(await screen.findByLabelText('Пароль'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Повторите пароль'), { target: { value: repeat } });
  act(() => {
    screen.getByRole('button', { name: 'Задать пароль и войти' }).click();
  });
}

beforeEach(() => {
  server.use(handlers.getMeError(401), handlers.getInvite(holder));
});

it('shows the loading state until the invite arrives', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.get(
      `/api/invites/${token}`,
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderInvite();

  const loading = await screen.findByText('Проверяем приглашение…');
  expect(loading).toHaveClass('loading');
  expect(loading.querySelector('.spinner')).not.toBeNull();
  act(() => {
    answer(HttpResponse.json(holder));
  });
  expect(await screen.findByLabelText('Пароль')).toBeInTheDocument();
});

it('on a valid link shows who it is for and the password form', async () => {
  renderInvite();

  expect(await screen.findByText(`Приглашение для ${holder.name} · ${holder.email}`)).toHaveClass(
    'hint',
  );
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Телеметрия агентов');
  expect(document.querySelector('div.вход-экран > div.card.вход-карта')).not.toBeNull();
  for (const label of ['Пароль', 'Повторите пароль']) {
    const input = screen.getByLabelText(label);
    expect(input).toHaveAttribute('type', 'password');
    expect(input).toHaveAttribute('autocomplete', 'new-password');
    expect(input).toHaveAttribute('maxlength', '128');
  }
  expect(screen.getByRole('button', { name: 'Задать пароль и войти' })).toHaveClass(
    'btn',
    'btn-primary',
  );
});

it.each([
  [404, linkNotFound],
  [410, linkUsed],
])('on %i shows the server message and a way to sign in', async (status, body) => {
  server.use(handlers.getInviteError(status, body));
  renderInvite();

  const empty = (await screen.findByText(body.message)).closest('.empty');
  expect(empty).not.toBeNull();
  const toLogin = screen.getByRole('link', { name: 'Ко входу' });
  expect(toLogin).toHaveClass('btn');
  expect(toLogin).toHaveAttribute('href', '/login');
  expect(screen.queryByLabelText('Пароль')).toBeNull();
});

it('does not send passwords that do not match', async () => {
  let sent = false;
  server.use(
    http.post(`/api/invites/${token}/accept`, () => {
      sent = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderInvite();

  await submit('длинный пароль', 'другой пароль');

  const repeat = screen.getByLabelText('Повторите пароль');
  expect(await screen.findByText('Пароли не совпадают')).toHaveClass('hint-err');
  expect(repeat).toHaveAttribute('aria-invalid', 'true');
  expect(sent).toBe(false);
});

it('does not send a short password', async () => {
  let sent = false;
  server.use(
    http.post(`/api/invites/${token}/accept`, () => {
      sent = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderInvite();

  await submit('1234567');

  expect(await screen.findByText('Не короче 8 символов')).toHaveClass('hint-err');
  expect(screen.getByLabelText('Пароль')).toHaveAttribute('aria-invalid', 'true');
  expect(sent).toBe(false);
});

it('shows a server error above the form', async () => {
  server.use(handlers.acceptInviteError(410, linkUsed));
  renderInvite();

  await submit('длинный пароль');

  const notice = await screen.findByRole('alert');
  expect(notice).toHaveTextContent(linkUsed.message);
  expect(notice).toHaveClass('notice', 'notice-warn');
  expect(screen.getByRole('button', { name: 'Задать пароль и войти' })).toBeEnabled();
});

it('on success sets the password, drops the cache and opens /users', async () => {
  let sent: unknown;
  server.use(
    http.post(`/api/invites/${token}/accept`, async ({ request }) => {
      sent = await request.json();
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderInvite();
  await screen.findByLabelText('Пароль');
  const clear = vi.spyOn(app.queryClient, 'clear');

  await submit('длинный пароль');

  expect(await screen.findByText('Пользователи', { selector: 'span.крупно' })).toBeInTheDocument();
  expect(app.router.state.location.href).toBe('/users');
  expect(sent).toEqual({ password: 'длинный пароль' });
  expect(clear).toHaveBeenCalledOnce();
  expect(await screen.findByTitle(`${me.name} — выйти`)).toBeInTheDocument();
});
