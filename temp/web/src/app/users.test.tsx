import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import type { components } from '../shared/api';
import { formatAgo, formatDateTime, fullMoment } from '../shared/lib/time';
import { handlers, meRow, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

type User = components['schemas']['UserListItem'];

function renderUsers() {
  const app = createApp(createMemoryHistory({ initialEntries: ['/users'] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

const active: User = {
  id: '0b8e4c1a-2d3f-4a5b-8c6d-7e8f9a0b1c2d',
  email: 'boris@example.test',
  name: 'Борис Иванов',
  status: 'active',
  createdAt: '2026-09-10T09:30:00Z',
  lastLoginAt: new Date(Date.now() - 5 * 60_000).toISOString(),
  inviteExpiresAt: null,
  isMe: false,
};

const neverSignedIn: User = {
  ...active,
  id: '1c9f5d2b-3e4a-4b6c-9d7e-8f9a0b1c2d3e',
  email: 'vera@example.test',
  name: 'Вера Смирнова',
  lastLoginAt: null,
};

const invited: User = {
  id: '2d0a6e3c-4f5b-4c7d-8e8f-9a0b1c2d3e4f',
  email: 'gleb@example.test',
  name: 'Глеб Орлов',
  status: 'invited',
  createdAt: '2026-09-29T12:00:00Z',
  lastLoginAt: null,
  inviteExpiresAt: '2026-10-06T12:00:00Z',
  isMe: false,
};

const row = async (name: string) => {
  const title = await screen.findByText(name, { selector: '.cell-title' });
  const tr = title.closest('tr');
  if (!tr) throw new Error(`no row for ${name}`);
  return tr;
};

const cells = (tr: HTMLElement) => [...tr.querySelectorAll('td')];

beforeEach(() => {
  server.use(handlers.getMe());
});

it('shows the Settings / Users crumbs and the page title', async () => {
  renderUsers();

  const title = await screen.findByText('Пользователи', { selector: 'span.крупно' });
  expect(title.parentElement).toHaveClass('row');
  expect(title.parentElement).toHaveStyle({ justifyContent: 'space-between' });
  const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
  expect(crumbs.querySelector('span.crumb-group')).toHaveTextContent('Настройки');
  expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Пользователи');
});

it('shows the loading state until the list arrives', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.get(
      '/api/users',
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderUsers();

  const loading = await screen.findByText('Загружаем пользователей…');
  expect(loading).toHaveClass('loading');
  expect(loading.querySelector('.spinner')).not.toBeNull();
  act(() => {
    answer(HttpResponse.json({ items: [meRow] }));
  });
  expect(await row(meRow.name)).toBeInTheDocument();
  expect(screen.queryByText('Загружаем пользователей…')).toBeNull();
});

it('lays the table out as deploy lists: table-wrap, table, the five columns', async () => {
  renderUsers();

  const tr = await row(meRow.name);
  const table = tr.closest('table');
  expect(table).toHaveClass('table');
  expect(table?.parentElement).toHaveClass('table-wrap');
  const heads = [...(table?.querySelectorAll('thead th') ?? [])];
  expect(heads.map((th) => th.textContent)).toEqual([
    'пользователь',
    'статус',
    'последний вход',
    'добавлен',
    '',
  ]);
  expect(heads.map((th) => th.className)).toEqual(['', '', 'nowrap', 'nowrap', 'nowrap']);
});

it('shows an active user: name, email, «активен», last sign-in ago, added date-time', async () => {
  server.use(handlers.getUsers([active]));
  renderUsers();

  const [user, status, lastLogin, added, actions] = cells(await row(active.name));
  expect(user?.querySelector('.cell-title')).toHaveTextContent(active.name);
  const email = user?.querySelector('div.faint');
  expect(email).toHaveTextContent(active.email);
  expect(email).toHaveStyle({ fontSize: '12.5px' });
  expect(user?.querySelector('.badge')).toBeNull();
  expect(within(status as HTMLElement).getByText('активен')).toHaveClass('badge', 'badge-ok');

  expect(lastLogin).toHaveClass('nowrap', 'faint');
  expect(lastLogin).toHaveTextContent('5 мин назад');
  const lastLoginAt = active.lastLoginAt ?? '';
  expect(lastLogin?.querySelector('time')).toHaveAttribute(
    'title',
    fullMoment(new Date(lastLoginAt)),
  );
  expect(formatAgo(new Date(lastLoginAt))).toBe('5 мин назад');

  expect(added).toHaveClass('nowrap', 'faint');
  const createdAt = new Date(active.createdAt);
  expect(added).toHaveTextContent(formatDateTime(createdAt));
  expect(added?.querySelector('time')).toHaveAttribute('title', fullMoment(createdAt));

  expect(actions).toHaveClass('nowrap');
});

it('shows «—» for an active user who never signed in', async () => {
  server.use(handlers.getUsers([neverSignedIn]));
  renderUsers();

  const lastLogin = cells(await row(neverSignedIn.name))[2];
  expect(lastLogin).toHaveTextContent(/^—$/);
});

it('shows an invited user: «приглашён» and the invitation expiry', async () => {
  server.use(handlers.getUsers([invited]));
  renderUsers();

  const [, status, lastLogin] = cells(await row(invited.name));
  expect(within(status as HTMLElement).getByText('приглашён')).toHaveClass('badge', 'badge-warn');
  const expiresAt = new Date(invited.inviteExpiresAt ?? '');
  expect(lastLogin).toHaveTextContent(`приглашение до ${formatDateTime(expiresAt)}`);
  expect(lastLogin?.querySelector('time')).toHaveAttribute('title', fullMoment(expiresAt));
});

it('marks the signed-in user «это вы» after the name', async () => {
  server.use(handlers.getUsers([meRow, active]));
  renderUsers();

  const mine = cells(await row(meRow.name))[0];
  const badge = within(mine as HTMLElement).getByText('это вы');
  expect(badge).toHaveClass('badge', 'badge-quiet');
  expect(badge.previousElementSibling).toHaveClass('cell-title');
  expect(within(cells(await row(active.name))[0] as HTMLElement).queryByText('это вы')).toBeNull();
});

it('shows the empty state for an empty list', async () => {
  server.use(handlers.getUsers([]));
  renderUsers();

  const empty = await screen.findByRole('heading', { level: 3, name: 'Пользователей пока нет' });
  expect(empty.parentElement).toHaveClass('empty');
});

it('on a load error shows the message and retries on «Повторить»', async () => {
  server.use(handlers.getUsersError(500, { code: 'internal', message: 'Сервис недоступен' }));
  renderUsers();

  const alert = await screen.findByRole('alert', {}, { timeout: 3000 });
  expect(alert).toHaveClass('notice', 'notice-err');
  expect(alert).toHaveTextContent('Сервис недоступен');

  server.use(handlers.getUsers([active]));
  act(() => {
    within(alert).getByRole('button', { name: 'Повторить' }).click();
  });

  expect(await row(active.name)).toBeInTheDocument();
  expect(screen.queryByRole('alert')).toBeNull();
});

// jsdom has no showModal/close; like the browser, close() fires «close».
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    if (!this.open) return;
    this.open = false;
    this.dispatchEvent(new Event('close'));
  };
});

describe('inviting a user', () => {
  const invitation: components['schemas']['InviteUserResponse'] = {
    user: invited,
    inviteUrl: 'http://localhost:8080/invite/example-token',
    expiresAt: '2026-10-07T12:00:00Z',
  };

  async function openDialog() {
    const button = await screen.findByRole('button', { name: 'Пригласить' });
    act(() => {
      button.click();
    });
    const dialog = screen.getByRole('dialog', { name: 'Пригласить пользователя' });
    expect(dialog).toHaveAttribute('open');
    return dialog;
  }

  function fillAndSubmit(dialog: HTMLElement, name = invited.name, email = invited.email) {
    fireEvent.change(within(dialog).getByLabelText('Имя'), { target: { value: name } });
    fireEvent.change(within(dialog).getByLabelText('Email'), { target: { value: email } });
    act(() => {
      within(dialog).getByRole('button', { name: 'Создать ссылку' }).click();
    });
  }

  it('puts «Пригласить» in the page header and lays the dialog out as the card says', async () => {
    renderUsers();

    const button = await screen.findByRole('button', { name: 'Пригласить' });
    expect(button).toHaveClass('btn', 'btn-primary');
    expect(button.parentElement).toBe(
      screen.getByText('Пользователи', { selector: 'span.крупно' }).parentElement,
    );

    const dialog = await openDialog();
    expect(dialog.tagName).toBe('DIALOG');
    expect(dialog).toHaveClass('окно');
    const name = within(dialog).getByLabelText('Имя');
    expect(name).toHaveAttribute('id', 'invite-name');
    expect(name).toBeRequired();
    expect(name).toHaveAttribute('maxlength', '100');
    const email = within(dialog).getByLabelText('Email');
    expect(email).toHaveAttribute('id', 'invite-email');
    expect(email).toHaveAttribute('type', 'email');
    expect(within(dialog).getByRole('button', { name: 'Отмена' }).className).toBe('btn');
    expect(within(dialog).getByRole('button', { name: 'Создать ссылку' })).toHaveClass(
      'btn',
      'btn-primary',
    );
  });

  it('on success shows the link once and asks for the list again', async () => {
    let items = [meRow];
    let sent: unknown;
    server.use(
      http.get('/api/users', () => HttpResponse.json({ items })),
      http.post('/api/users', async ({ request }) => {
        sent = await request.json();
        items = [meRow, invited];
        return HttpResponse.json(invitation, { status: 201 });
      }),
    );
    renderUsers();
    await row(meRow.name);

    const dialog = await openDialog();
    fillAndSubmit(dialog);

    expect(
      await within(dialog).findByText(
        `Ссылка для ${invited.name}. Она действует до ${formatDateTime(new Date(invitation.expiresAt))} и сработает один раз. Передайте её сами — писем система не отправляет.`,
      ),
    ).toBeInTheDocument();
    expect(sent).toEqual({ name: invited.name, email: invited.email });
    const link = within(dialog).getByText(invitation.inviteUrl);
    expect(link.closest('.код')).not.toBeNull();
    expect(within(dialog).queryByLabelText('Имя')).toBeNull();

    const status = cells(await row(invited.name))[1];
    expect(within(status as HTMLElement).getByText('приглашён')).toBeInTheDocument();

    const done = within(dialog).getByRole('button', { name: 'Готово' });
    expect(done).toHaveClass('btn', 'btn-primary');
    act(() => {
      done.click();
    });
    expect(dialog).not.toHaveAttribute('open');
  });

  it('shows email_taken under the email field', async () => {
    server.use(
      handlers.inviteUserError(409, {
        code: 'email_taken',
        message: 'Пользователь с таким email уже есть',
      }),
    );
    renderUsers();

    const dialog = await openDialog();
    fillAndSubmit(dialog);

    const email = within(dialog).getByLabelText('Email');
    await vi.waitFor(() => {
      expect(email).toHaveAccessibleDescription('Пользователь с таким email уже есть');
    });
    expect(email).toHaveAttribute('aria-invalid', 'true');
    expect(within(dialog).getByLabelText('Имя')).not.toHaveAttribute('aria-invalid');
    expect(within(dialog).queryByRole('alert')).toBeNull();
  });

  it('shows invalid_name under the name field', async () => {
    server.use(handlers.inviteUserError(400, { code: 'invalid_name', message: 'Укажите имя' }));
    renderUsers();

    const dialog = await openDialog();
    fillAndSubmit(dialog);

    const name = within(dialog).getByLabelText('Имя');
    await vi.waitFor(() => {
      expect(name).toHaveAccessibleDescription('Укажите имя');
    });
    expect(within(dialog).getByLabelText('Email')).not.toHaveAttribute('aria-invalid');
  });

  it('shows any other error as a notice in the dialog', async () => {
    server.use(handlers.inviteUserError(500, { code: 'internal', message: 'Сервис недоступен' }));
    renderUsers();

    const dialog = await openDialog();
    fillAndSubmit(dialog);

    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveClass('notice', 'notice-err');
    expect(alert).toHaveTextContent('Сервис недоступен');
    expect(within(dialog).getByLabelText('Email')).not.toHaveAttribute('aria-invalid');
  });

  it('«Отмена» closes the dialog and the next opening starts with an empty form', async () => {
    server.use(
      handlers.inviteUserError(409, {
        code: 'email_taken',
        message: 'Пользователь с таким email уже есть',
      }),
    );
    renderUsers();

    let dialog = await openDialog();
    fillAndSubmit(dialog);
    await vi.waitFor(() => {
      expect(within(dialog).getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true');
    });

    act(() => {
      within(dialog).getByRole('button', { name: 'Отмена' }).click();
    });
    expect(dialog).not.toHaveAttribute('open');

    dialog = await openDialog();
    expect(within(dialog).getByLabelText('Имя')).toHaveValue('');
    expect(within(dialog).getByLabelText('Email')).toHaveValue('');
    expect(within(dialog).getByLabelText('Email')).not.toHaveAttribute('aria-invalid');
  });

  it('closing after success starts the next invite with an empty form', async () => {
    server.use(handlers.inviteUser(invitation));
    renderUsers();

    let dialog = await openDialog();
    fillAndSubmit(dialog);
    const done = await within(dialog).findByRole('button', { name: 'Готово' });
    act(() => {
      done.click();
    });

    dialog = await openDialog();
    expect(within(dialog).queryByText(invitation.inviteUrl)).toBeNull();
    expect(within(dialog).getByLabelText('Имя')).toHaveValue('');
  });
});

describe('row actions', () => {
  const buttonNames = (tr: HTMLElement) =>
    within(cells(tr)[4] as HTMLElement)
      .queryAllByRole('button')
      .map((button) => button.textContent);

  async function openAction(user: User, name: string, dialogName = name) {
    const tr = await row(user.name);
    act(() => {
      within(cells(tr)[4] as HTMLElement)
        .getByRole('button', { name })
        .click();
    });
    const dialog = screen.getByRole('dialog', { name: dialogName });
    expect(dialog).toHaveAttribute('open');
    expect(dialog).toHaveClass('окно');
    return dialog;
  }

  // A list that answers from `items`, counting how often it was asked.
  function serveUsers(initial: User[]) {
    const state = { items: initial, asked: 0 };
    server.use(
      http.get('/api/users', () => {
        state.asked += 1;
        return HttpResponse.json({ items: state.items });
      }),
    );
    return state;
  }

  it('offers each kind of row its own buttons, btn btn-sm, and none on your own row', async () => {
    server.use(handlers.getUsers([meRow, active, invited]));
    renderUsers();

    expect(buttonNames(await row(invited.name))).toEqual(['Новая ссылка', 'Отозвать']);
    expect(buttonNames(await row(active.name))).toEqual(['Ссылка сброса пароля']);
    expect(buttonNames(await row(meRow.name))).toEqual([]);
    for (const button of within(cells(await row(invited.name))[4] as HTMLElement).getAllByRole(
      'button',
    )) {
      expect(button.className).toBe('btn btn-sm');
    }
    expect(
      within(cells(await row(active.name))[4] as HTMLElement).getByRole('button').className,
    ).toBe('btn btn-sm');
  });

  it('«Новая ссылка»: confirm, show the new link once, refresh the list', async () => {
    const link: components['schemas']['InviteLink'] = {
      inviteUrl: 'http://localhost:8080/invite/new-token',
      expiresAt: '2026-10-08T09:00:00Z',
    };
    const users = serveUsers([meRow, invited]);
    let reissued = '';
    server.use(
      http.post('/api/users/:id/invite', ({ params }) => {
        reissued = String(params.id);
        return HttpResponse.json(link);
      }),
    );
    renderUsers();

    const dialog = await openAction(invited, 'Новая ссылка');
    expect(
      within(dialog).getByText(`Старая ссылка для ${invited.name} перестанет работать.`),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Отмена' }).className).toBe('btn');
    const confirm = within(dialog).getByRole('button', { name: 'Выпустить новую' });
    expect(confirm).toHaveClass('btn', 'btn-primary');
    const askedBefore = users.asked;
    act(() => {
      confirm.click();
    });

    const url = await within(dialog).findByText(link.inviteUrl);
    expect(url.closest('.код')).not.toBeNull();
    expect(reissued).toBe(invited.id);
    expect(
      within(dialog).getByText(
        `Действует до ${formatDateTime(new Date(link.expiresAt))}, сработает один раз.`,
      ),
    ).toBeInTheDocument();
    await vi.waitFor(() => {
      expect(users.asked).toBeGreaterThan(askedBefore);
    });

    const done = within(dialog).getByRole('button', { name: 'Готово' });
    expect(done).toHaveClass('btn', 'btn-primary');
    act(() => {
      done.click();
    });
    expect(dialog).not.toHaveAttribute('open');
    expect(within(dialog).queryByText(link.inviteUrl)).toBeNull();
  });

  it('«Отозвать»: confirm with the name and email, then the user leaves the list', async () => {
    const users = serveUsers([meRow, invited]);
    let revoked = '';
    server.use(
      http.delete('/api/users/:id', ({ params }) => {
        revoked = String(params.id);
        users.items = [meRow];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderUsers();

    const dialog = await openAction(invited, 'Отозвать', 'Отозвать приглашение');
    expect(
      within(dialog).getByText(
        `Отозвать приглашение ${invited.name} (${invited.email})? Ссылка перестанет работать, пользователь исчезнет из списка.`,
      ),
    ).toBeInTheDocument();
    const confirm = within(dialog).getByRole('button', { name: 'Отозвать' });
    expect(confirm.className).toBe('btn плохо');
    act(() => {
      confirm.click();
    });

    await vi.waitFor(() => {
      expect(screen.queryByText(invited.name, { selector: '.cell-title' })).toBeNull();
    });
    expect(revoked).toBe(invited.id);
    expect(dialog).not.toHaveAttribute('open');
    expect(await row(meRow.name)).toBeInTheDocument();
  });

  it('«Ссылка сброса пароля»: confirm, show the reset link once, refresh the list', async () => {
    const link: components['schemas']['ResetLink'] = {
      resetUrl: 'http://localhost:8080/reset/reset-token',
      expiresAt: '2026-10-07T10:00:00Z',
    };
    const users = serveUsers([meRow, active]);
    let issued = '';
    server.use(
      http.post('/api/users/:id/password-reset', ({ params }) => {
        issued = String(params.id);
        return HttpResponse.json(link);
      }),
    );
    renderUsers();

    const dialog = await openAction(active, 'Ссылка сброса пароля');
    expect(
      within(dialog).getByText(
        `Выдать ${active.name} ссылку для нового пароля? Когда ей воспользуются, все входы ${active.name} закроются.`,
      ),
    ).toBeInTheDocument();
    const confirm = within(dialog).getByRole('button', { name: 'Выдать ссылку' });
    expect(confirm).toHaveClass('btn', 'btn-primary');
    const askedBefore = users.asked;
    act(() => {
      confirm.click();
    });

    const url = await within(dialog).findByText(link.resetUrl);
    expect(url.closest('.код')).not.toBeNull();
    expect(issued).toBe(active.id);
    expect(
      within(dialog).getByText(
        `Действует до ${formatDateTime(new Date(link.expiresAt))}, сработает один раз. Прежняя неиспользованная ссылка сброса перестала работать.`,
      ),
    ).toBeInTheDocument();
    await vi.waitFor(() => {
      expect(users.asked).toBeGreaterThan(askedBefore);
    });

    act(() => {
      within(dialog).getByRole('button', { name: 'Готово' }).click();
    });
    expect(dialog).not.toHaveAttribute('open');
  });

  it('on a 409 shows the message in the dialog and asks for the list again', async () => {
    const users = serveUsers([meRow, invited]);
    server.use(
      http.post('/api/users/:id/invite', () => {
        users.items = [meRow, { ...invited, status: 'active', inviteExpiresAt: null }];
        return HttpResponse.json(
          { code: 'user_not_invited', message: 'Пользователь уже принял приглашение' },
          { status: 409 },
        );
      }),
    );
    renderUsers();

    const dialog = await openAction(invited, 'Новая ссылка');
    act(() => {
      within(dialog).getByRole('button', { name: 'Выпустить новую' }).click();
    });

    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveClass('notice', 'notice-err');
    expect(alert).toHaveTextContent('Пользователь уже принял приглашение');
    // The refreshed list shows the user as active while the dialog keeps its error.
    await vi.waitFor(async () => {
      expect(buttonNames(await row(invited.name))).toEqual(['Ссылка сброса пароля']);
    });
    expect(dialog).toHaveAttribute('open');
    expect(within(dialog).getByRole('alert')).toBeInTheDocument();
  });

  it('blocks the confirm button while the request is on its way', async () => {
    let answer: (response: Response) => void = () => undefined;
    serveUsers([meRow, active]);
    server.use(
      http.post(
        '/api/users/:id/password-reset',
        () =>
          new Promise<Response>((resolve) => {
            answer = resolve;
          }),
      ),
    );
    renderUsers();

    const dialog = await openAction(active, 'Ссылка сброса пароля');
    const confirm = within(dialog).getByRole('button', { name: 'Выдать ссылку' });
    act(() => {
      confirm.click();
    });
    await vi.waitFor(() => {
      expect(confirm).toBeDisabled();
    });
    act(() => {
      answer(
        HttpResponse.json({
          resetUrl: 'http://localhost:8080/reset/late-token',
          expiresAt: '2026-10-07T10:00:00Z',
        }),
      );
    });
    expect(
      await within(dialog).findByText('http://localhost:8080/reset/late-token'),
    ).toBeInTheDocument();
  });

  it('on a 404 shows the message in the dialog of the reset link', async () => {
    serveUsers([meRow, active]);
    server.use(
      http.post('/api/users/:id/password-reset', () =>
        HttpResponse.json(
          { code: 'user_not_found', message: 'Пользователь не найден' },
          { status: 404 },
        ),
      ),
    );
    renderUsers();

    const dialog = await openAction(active, 'Ссылка сброса пароля');
    act(() => {
      within(dialog).getByRole('button', { name: 'Выдать ссылку' }).click();
    });

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Пользователь не найден');
  });
});
