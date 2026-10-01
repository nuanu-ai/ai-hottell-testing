import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import type { components } from '../shared/api';
import { handlers, me, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

type ChangePasswordRequest = components['schemas']['ChangePasswordRequest'];

const wrongCurrent = {
  code: 'wrong_current_password',
  message: 'Текущий пароль указан неверно',
};

function renderProfile() {
  const app = createApp(createMemoryHistory({ initialEntries: ['/profile'] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

async function submit(current: string, password: string, repeat = password) {
  fireEvent.change(await screen.findByLabelText('Текущий пароль'), {
    target: { value: current },
  });
  fireEvent.change(screen.getByLabelText('Новый пароль'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Повторите новый пароль'), {
    target: { value: repeat },
  });
  act(() => {
    screen.getByRole('button', { name: 'Сменить пароль' }).click();
  });
}

beforeEach(() => {
  server.use(handlers.getMe());
});

it('shows who is signed in and the password card', async () => {
  renderProfile();

  expect(await screen.findByText(me.name, { selector: 'span.крупно' })).toBeInTheDocument();
  const email = screen.getByText(me.email);
  expect(email.tagName).toBe('DIV');
  expect(email).toHaveClass('faint');
  expect(email).toHaveStyle({ fontSize: '13px' });

  const heading = screen.getByRole('heading', { level: 2, name: 'Пароль' });
  const card = heading.closest('.card');
  expect(card).not.toBeNull();
  expect(
    within(card as HTMLElement).getByText(
      'Смена пароля закроет все остальные входы в аккаунт — этот останется',
    ),
  ).toHaveClass('hint');
  expect(within(card as HTMLElement).getByRole('button', { name: 'Сменить пароль' })).toHaveClass(
    'btn',
    'btn-primary',
  );
  expect(screen.getByLabelText('Текущий пароль')).toHaveAttribute(
    'autocomplete',
    'current-password',
  );
  for (const label of ['Текущий пароль', 'Новый пароль', 'Повторите новый пароль']) {
    expect(screen.getByLabelText(label)).toHaveAttribute('type', 'password');
  }
  for (const label of ['Новый пароль', 'Повторите новый пароль']) {
    expect(screen.getByLabelText(label)).toHaveAttribute('autocomplete', 'new-password');
  }
});

it('on success clears the fields and says the other sign-ins are closed', async () => {
  let sent: ChangePasswordRequest | undefined;
  server.use(
    http.post('/api/me/password', async ({ request }) => {
      sent = (await request.json()) as ChangePasswordRequest;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderProfile();

  await submit('старый пароль', 'новый длинный пароль');

  const done = await screen.findByText('Пароль изменён. Остальные входы закрыты');
  expect(done).toHaveClass('notice', 'notice-ok');
  expect(sent).toEqual({ currentPassword: 'старый пароль', newPassword: 'новый длинный пароль' });
  for (const label of ['Текущий пароль', 'Новый пароль', 'Повторите новый пароль']) {
    expect(screen.getByLabelText(label)).toHaveValue('');
  }
});

it('shows a wrong current password under its field', async () => {
  server.use(handlers.changePasswordError(400, wrongCurrent));
  renderProfile();

  await submit('не тот пароль', 'новый длинный пароль');

  const error = await screen.findByText(wrongCurrent.message);
  expect(error).toHaveClass('hint', 'hint-err');
  const current = screen.getByLabelText('Текущий пароль');
  expect(current).toHaveAttribute('aria-invalid', 'true');
  expect(current).toHaveAttribute('aria-describedby', error.id);
  expect(document.querySelector('.notice-err')).toBeNull();
  expect(screen.getByLabelText('Новый пароль')).toHaveValue('новый длинный пароль');
});

it('shows any other server error as a notice', async () => {
  const tooShort = {
    code: 'password_too_short',
    message: 'Пароль должен быть не короче 8 символов',
  };
  server.use(handlers.changePasswordError(400, tooShort));
  renderProfile();

  await submit('старый пароль', 'новый длинный пароль');

  expect(await screen.findByText(tooShort.message)).toHaveClass('notice', 'notice-err');
  expect(screen.getByLabelText('Текущий пароль')).not.toHaveAttribute('aria-invalid');
});

it('does not send new passwords that do not match', async () => {
  let sent = false;
  server.use(
    http.post('/api/me/password', () => {
      sent = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderProfile();

  await submit('старый пароль', 'новый длинный пароль', 'другой длинный пароль');

  expect(await screen.findByText('Пароли не совпадают')).toHaveClass('hint-err');
  expect(screen.getByLabelText('Повторите новый пароль')).toHaveAttribute('aria-invalid', 'true');
  expect(sent).toBe(false);
});

it('does not send a short new password', async () => {
  let sent = false;
  server.use(
    http.post('/api/me/password', () => {
      sent = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderProfile();

  await submit('старый пароль', 'short');

  expect(await screen.findByText('Не короче 8 символов')).toHaveClass('hint-err');
  expect(screen.getByLabelText('Новый пароль')).toHaveAttribute('aria-invalid', 'true');
  expect(sent).toBe(false);
});
