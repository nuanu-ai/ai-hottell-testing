import { browserSupportsWebAuthn, startAuthentication } from '@simplewebauthn/browser';
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import { handlers, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}));

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

const failure = { code: 'internal', message: 'Сервис недоступен' };

const passkeyButton = () => screen.findByRole('button', { name: 'Войти по passkey' });

async function clickPasskey() {
  const button = await passkeyButton();
  act(() => {
    button.click();
  });
  return button;
}

beforeEach(() => {
  vi.clearAllMocks();
  server.use(handlers.getMeError(401));
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true);
  vi.mocked(startAuthentication).mockResolvedValue({ id: 'cred' } as never);
});

afterEach(() => {
  vi.restoreAllMocks();
});

it('puts the fingerprint button after «Войти» in the login actions', async () => {
  renderAt('/login');

  const button = await passkeyButton();
  expect(button).toHaveAttribute('type', 'button');
  expect(button).toHaveAttribute('title', 'Войти по passkey');
  expect(button).toHaveClass('btn-passkey');
  expect(button).toBeEnabled();
  expect(button.parentElement).toHaveClass('login-actions');
  expect(button.previousElementSibling).toHaveTextContent('Войти');
  expect(button.querySelector('span.pk-icon > svg.pk-print')).not.toBeNull();
  const lines = button.querySelectorAll('path.pk-line');
  expect(lines.length).toBeGreaterThan(0);
  lines.forEach((line) => {
    expect(line).toHaveAttribute('pathLength', '1');
  });
});

it('begins, asks the browser, finishes and goes to next', async () => {
  let finished: unknown;
  server.use(
    handlers.passkeyLoginBegin({ challenge: 'abc' }),
    http.post('/api/auth/passkey/login/finish', async ({ request }) => {
      finished = await request.json();
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderAt('/login?next=%2Fprofile');

  await clickPasskey();

  expect(await screen.findByRole('heading', { level: 2, name: 'Пароль' })).toBeInTheDocument();
  expect(href(app)).toBe('/profile');
  expect(startAuthentication).toHaveBeenCalledWith({ optionsJSON: { challenge: 'abc' } });
  expect(finished).toEqual({ credential: { id: 'cred' } });
});

it('goes to /users without next', async () => {
  server.use(
    handlers.passkeyLoginBegin(),
    http.post('/api/auth/passkey/login/finish', () => {
      server.use(handlers.getMe());
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const app = renderAt('/login');

  await clickPasskey();

  await waitFor(() => {
    expect(href(app)).toBe('/users');
  });
});

it('scans and blocks both buttons while the browser waits', async () => {
  let answer: (value: never) => void = () => undefined;
  vi.mocked(startAuthentication).mockReturnValue(
    new Promise((resolve) => {
      answer = resolve;
    }),
  );
  server.use(handlers.passkeyLoginBegin(), handlers.passkeyLoginFinishError(500, failure));
  renderAt('/login');

  const button = await clickPasskey();

  await waitFor(() => {
    expect(button).toHaveClass('btn-passkey', 'scanning');
  });
  expect(button).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Войти' })).toBeDisabled();
  act(() => {
    answer({ id: 'cred' } as never);
  });
  await waitFor(() => {
    expect(button).not.toHaveClass('scanning');
  });
  expect(button).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Войти' })).toBeEnabled();
});

it('says the sign-in was cancelled when the prompt is closed', async () => {
  vi.mocked(startAuthentication).mockRejectedValue(new DOMException('closed', 'NotAllowedError'));
  server.use(handlers.passkeyLoginBegin());
  const app = renderAt('/login');

  await clickPasskey();

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('Вход по passkey отменён');
  expect(alert).toHaveClass('notice', 'notice-warn');
  expect(href(app)).toBe('/login');
});

it('shows the server message when finish fails', async () => {
  server.use(
    handlers.passkeyLoginBegin(),
    handlers.passkeyLoginFinishError(400, {
      code: 'passkey_verification_failed',
      message: 'Passkey не подтверждён',
    }),
  );
  renderAt('/login');

  await clickPasskey();

  expect(await screen.findByRole('alert')).toHaveTextContent('Passkey не подтверждён');
});

it('shows the server message when begin fails and does not ask the browser', async () => {
  server.use(handlers.passkeyLoginBeginError(500, failure));
  renderAt('/login');

  await clickPasskey();

  expect(await screen.findByRole('alert')).toHaveTextContent('Сервис недоступен');
  expect(startAuthentication).not.toHaveBeenCalled();
});

it('asks to try again when the browser fails otherwise', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
  vi.mocked(startAuthentication).mockRejectedValue(new DOMException('no', 'SecurityError'));
  server.use(handlers.passkeyLoginBegin());
  renderAt('/login');

  await clickPasskey();

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Не удалось войти по passkey. Попробуйте ещё раз',
  );
});

it('disables the button in a browser without WebAuthn', async () => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(false);
  renderAt('/login');

  const button = await passkeyButton();

  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('title', 'Этот браузер не поддерживает passkey');
  expect(screen.getByRole('button', { name: 'Войти' })).toBeEnabled();
});
