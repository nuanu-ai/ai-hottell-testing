import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import { handlers, me, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

function renderAt(path: string) {
  const app = createApp(createMemoryHistory({ initialEntries: [path] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

const href = (app: ReturnType<typeof createApp>) => app.router.state.location.href;

async function signIn(password = 'верный пароль') {
  const email = await screen.findByLabelText('Email');
  const passwordInput = screen.getByLabelText('Пароль');
  fireEvent.change(email, { target: { value: me.email } });
  fireEvent.change(passwordInput, { target: { value: password } });
  act(() => {
    screen.getByRole('button', { name: 'Войти' }).click();
  });
}

beforeEach(() => {
  server.use(handlers.getMeError(401));
});

it('shows the sign-in form with the email field focused', async () => {
  renderAt('/login');

  const email = await screen.findByLabelText('Email');
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Телеметрия агентов');
  expect(email).toHaveAttribute('type', 'email');
  expect(email).toHaveAttribute('autocomplete', 'username');
  expect(email).toHaveFocus();
  expect(screen.getByLabelText('Пароль')).toHaveAttribute('autocomplete', 'current-password');
  expect(screen.queryByRole('button', { name: /тем/i })).toBeNull();
});

it('sends the credentials and goes to next', async () => {
  let sent: unknown;
  server.use(
    http.post('/api/auth/login', async ({ request }) => {
      sent = await request.json();
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderAt('/login?next=%2Fprofile');

  await signIn();

  expect(await screen.findByRole('heading', { level: 2, name: 'Пароль' })).toBeInTheDocument();
  expect(href(app)).toBe('/profile');
  expect(sent).toEqual({ email: me.email, password: 'верный пароль' });
});

it('sends next=//evil.example to /users', async () => {
  server.use(
    http.post('/api/auth/login', () => {
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderAt('/login?next=%2F%2Fevil.example');

  await signIn();

  expect(await screen.findByText('Пользователи', { selector: 'span.крупно' })).toBeInTheDocument();
  expect(href(app)).toBe('/users');
});

it('blocks the button and says «Входим…» while the request runs', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.post(
      '/api/auth/login',
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderAt('/login');

  await signIn();

  const button = await screen.findByRole('button', { name: 'Входим…' });
  expect(button).toBeDisabled();
  act(() => {
    answer(HttpResponse.json({ code: 'internal', message: 'Сервис недоступен' }, { status: 500 }));
  });
  expect(await screen.findByRole('button', { name: 'Войти' })).toBeEnabled();
});

it('on 401 shows the message, clears the password and focuses it', async () => {
  server.use(
    handlers.loginError(401, {
      code: 'invalid_credentials',
      message: 'Неверный email или пароль',
    }),
  );
  const app = renderAt('/login?next=%2Fusers');

  await signIn('неверный');

  expect(await screen.findByRole('alert')).toHaveTextContent('Неверный email или пароль');
  expect(screen.getByRole('alert')).toHaveClass('notice', 'notice-warn');
  const password = screen.getByLabelText('Пароль');
  expect(password).toHaveValue('');
  expect(password).toHaveFocus();
  expect(screen.getByLabelText('Email')).toHaveValue(me.email);
  expect(href(app)).toBe('/login?next=%2Fusers');
});

it('on another error shows its message and keeps the password', async () => {
  server.use(handlers.loginError(500, { code: 'internal', message: 'Сервис недоступен' }));
  renderAt('/login');

  await signIn();

  expect(await screen.findByRole('alert')).toHaveTextContent('Сервис недоступен');
  await waitFor(() => {
    expect(screen.getByLabelText('Пароль')).toHaveValue('верный пароль');
  });
});
