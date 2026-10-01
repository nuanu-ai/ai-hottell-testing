import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser';
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import type { components } from '../shared/api';
import { handlers, server } from '../shared/api/test/server';
import { formatDateTime } from '../shared/lib/time';
import { createApp } from './createApp';
import { AppProviders } from './providers';

type Passkey = components['schemas']['PasskeyItem'];

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}));

const macbook: Passkey = {
  id: '0b8e7f2c-1d3a-4c5b-9e6f-7a8b9c0d1e2f',
  name: 'MacBook',
  createdAt: '2026-09-20T10:15:00Z',
  lastUsedAt: null,
};

const iphone: Passkey = {
  id: '1c9f8a3d-2e4b-4d6c-8f7a-8b9c0d1e2f3a',
  name: 'iPhone',
  createdAt: '2026-09-21T11:00:00Z',
  lastUsedAt: '2026-09-29T09:00:00Z',
};

const failure = { code: 'internal', message: 'Сервис недоступен' };

function renderProfile() {
  const app = createApp(createMemoryHistory({ initialEntries: ['/profile'] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

async function passkeyCard() {
  const heading = await screen.findByRole('heading', { level: 2, name: 'Passkey' });
  const card = heading.closest('.card');
  expect(card).not.toBeNull();
  return card as HTMLElement;
}

// A list that answers from `items`, counting how often it was asked.
function servePasskeys(initial: Passkey[]) {
  const state = { items: initial, asked: 0 };
  server.use(
    http.get('/api/me/passkeys', () => {
      state.asked += 1;
      return HttpResponse.json({ items: state.items });
    }),
  );
  return state;
}

function add(name: string) {
  fireEvent.change(screen.getByLabelText('Название'), { target: { value: name } });
  act(() => {
    screen.getByRole('button', { name: 'Добавить passkey' }).click();
  });
}

const row = async (name: string) => {
  const cell = await screen.findByText(name, { selector: '.cell-title' });
  return cell.closest('tr') as HTMLElement;
};

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

beforeEach(() => {
  vi.clearAllMocks();
  server.use(handlers.getMe());
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true);
  vi.mocked(startRegistration).mockResolvedValue({ id: 'new-cred' } as never);
});

afterEach(() => {
  vi.restoreAllMocks();
});

it('lays the card out as the task says', async () => {
  server.use(handlers.getPasskeys([macbook]));
  renderProfile();

  const card = await passkeyCard();
  expect(
    within(card).getByText(
      'Входите без пароля: отпечатком, лицом или ключом. Пароль при этом продолжает работать',
    ),
  ).toHaveClass('hint');
  await row(macbook.name);
  const headers = within(card)
    .getAllByRole('columnheader')
    .map((th) => th.textContent);
  expect(headers).toEqual(['название', 'добавлен', 'последний вход', '']);

  const input = within(card).getByLabelText('Название');
  expect(input).toHaveAttribute('id', 'passkey-name');
  expect(input).toHaveAttribute('placeholder', 'Например, MacBook или iPhone');
  expect(input).toHaveAttribute('maxlength', '64');
  const button = within(card).getByRole('button', { name: 'Добавить passkey' });
  expect(button).toHaveClass('btn', 'btn-primary');
  const form = input.closest('.row');
  expect(form).not.toBeNull();
  expect(form).toContainElement(button);
  expect(card.querySelector('.table-wrap')?.compareDocumentPosition(form as Node)).toBe(
    Node.DOCUMENT_POSITION_FOLLOWING,
  );
});

it('lists the passkeys with when they were added and last used', async () => {
  server.use(handlers.getPasskeys([macbook, iphone]));
  renderProfile();

  const unused = await row(macbook.name);
  const cells = within(unused).getAllByRole('cell');
  expect(cells[1]).toHaveTextContent(formatDateTime(new Date(macbook.createdAt)));
  expect(cells[2]).toHaveTextContent('ещё не использовался');
  const remove = within(cells[3] as HTMLElement).getByRole('button', { name: 'Удалить' });
  expect(remove).toHaveClass('btn', 'btn-sm');

  const used = await row(iphone.name);
  const time = within(used).getAllByRole('cell')[2]?.querySelector('time');
  expect(time).toHaveAttribute('dateTime', iphone.lastUsedAt);
});

it('says there are no passkeys yet', async () => {
  renderProfile();

  const card = await passkeyCard();
  const empty = await within(card).findByText('Passkey ещё нет');
  expect(empty.closest('.empty')).toHaveTextContent('Добавьте, чтобы входить без пароля');
  expect(card.querySelector('table')).toBeNull();
});

it('begins with the name, asks the browser, finishes and asks the list again', async () => {
  const list = servePasskeys([]);
  let begun: unknown;
  let finished: unknown;
  server.use(
    http.post('/api/me/passkeys/register/begin', async ({ request }) => {
      begun = await request.json();
      return HttpResponse.json({ options: { challenge: 'abc' } });
    }),
    http.post('/api/me/passkeys/register/finish', async ({ request }) => {
      finished = await request.json();
      list.items = [macbook];
      return HttpResponse.json(macbook);
    }),
  );
  renderProfile();
  await screen.findByText('Passkey ещё нет');

  add('MacBook');

  const done = await screen.findByText('Passkey «MacBook» добавлен');
  expect(done).toHaveClass('notice', 'notice-ok');
  expect(begun).toEqual({ name: 'MacBook' });
  expect(startRegistration).toHaveBeenCalledWith({ optionsJSON: { challenge: 'abc' } });
  expect(finished).toEqual({ credential: { id: 'new-cred' } });
  expect(screen.getByLabelText('Название')).toHaveValue('');
  expect(await row(macbook.name)).toBeInTheDocument();
  expect(list.asked).toBe(2);
});

it('waits for the browser with the button blocked', async () => {
  let answer: (value: never) => void = () => undefined;
  vi.mocked(startRegistration).mockReturnValue(
    new Promise((resolve) => {
      answer = resolve;
    }),
  );
  server.use(handlers.passkeyRegisterBegin(), handlers.passkeyRegisterFinishError(500, failure));
  renderProfile();
  await passkeyCard();

  add('MacBook');

  const waiting = await screen.findByRole('button', { name: 'Ждём подтверждения…' });
  expect(waiting).toBeDisabled();
  expect(screen.getByLabelText('Название')).toBeDisabled();
  act(() => {
    answer({ id: 'new-cred' } as never);
  });
  expect(await screen.findByRole('button', { name: 'Добавить passkey' })).toBeEnabled();
  expect(await screen.findByText(failure.message)).toHaveClass('notice', 'notice-err');
});

it('does not begin without a name', async () => {
  let begun = false;
  server.use(
    http.post('/api/me/passkeys/register/begin', () => {
      begun = true;
      return HttpResponse.json({ options: {} });
    }),
  );
  renderProfile();
  await passkeyCard();

  add('   ');

  expect(await screen.findByText('Введите название')).toHaveClass('hint', 'hint-err');
  expect(screen.getByLabelText('Название')).toHaveAttribute('aria-invalid', 'true');
  expect(begun).toBe(false);
});

it('says the adding was cancelled when the prompt is closed', async () => {
  vi.mocked(startRegistration).mockRejectedValue(new DOMException('closed', 'NotAllowedError'));
  server.use(handlers.passkeyRegisterBegin());
  renderProfile();
  await passkeyCard();

  add('MacBook');

  expect(await screen.findByText('Добавление отменено')).toHaveClass('notice', 'notice-warn');
  expect(screen.getByLabelText('Название')).toHaveValue('MacBook');
});

it('says the passkey is already added when the authenticator holds one', async () => {
  vi.mocked(startRegistration).mockRejectedValue(new DOMException('exists', 'InvalidStateError'));
  server.use(handlers.passkeyRegisterBegin());
  renderProfile();
  await passkeyCard();

  add('MacBook');

  expect(await screen.findByText('Этот passkey уже добавлен')).toHaveClass('notice', 'notice-warn');
});

it('shows the server message when begin fails and does not ask the browser', async () => {
  server.use(handlers.passkeyRegisterBeginError(500, failure));
  renderProfile();
  await passkeyCard();

  add('MacBook');

  expect(await screen.findByText(failure.message)).toHaveClass('notice', 'notice-err');
  expect(startRegistration).not.toHaveBeenCalled();
});

it('shows a rejected name under the field', async () => {
  const badName = { code: 'invalid_passkey_name', message: 'Название — от 1 до 64 символов' };
  server.use(handlers.passkeyRegisterBeginError(400, badName));
  renderProfile();
  await passkeyCard();

  add('MacBook');

  expect(await screen.findByText(badName.message)).toHaveClass('hint', 'hint-err');
  expect(document.querySelector('.notice-err')).toBeNull();
});

it('hides the form in a browser without WebAuthn', async () => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(false);
  server.use(handlers.getPasskeys([macbook]));
  renderProfile();

  const card = await passkeyCard();
  expect(within(card).getByText('Этот браузер не поддерживает passkey')).toHaveClass(
    'notice',
    'notice-info',
  );
  expect(within(card).queryByLabelText('Название')).toBeNull();
  expect(within(card).queryByRole('button', { name: 'Добавить passkey' })).toBeNull();
  expect(await row(macbook.name)).toBeInTheDocument();
});

describe('deleting a passkey', () => {
  async function openDelete(name: string) {
    const tr = await row(name);
    act(() => {
      within(tr).getByRole('button', { name: 'Удалить' }).click();
    });
    const dialog = screen.getByRole('dialog', { name: 'Удалить passkey' });
    expect(dialog).toHaveAttribute('open');
    expect(dialog).toHaveClass('окно');
    return dialog;
  }

  it('asks first, deletes and refreshes the list', async () => {
    const list = servePasskeys([macbook, iphone]);
    let deleted: string | undefined;
    server.use(
      http.delete('/api/me/passkeys/:id', ({ params }) => {
        deleted = params.id as string;
        list.items = [iphone];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderProfile();

    const dialog = await openDelete(macbook.name);
    expect(dialog).toHaveTextContent('Удалить passkey «MacBook»? Войти им больше не получится.');
    const confirm = within(dialog).getByRole('button', { name: 'Удалить' });
    expect(confirm).toHaveClass('btn', 'плохо');
    act(() => {
      confirm.click();
    });

    await waitFor(() => {
      expect(dialog).not.toHaveAttribute('open');
    });
    expect(deleted).toBe(macbook.id);
    await waitFor(() => {
      expect(screen.queryByText(macbook.name, { selector: '.cell-title' })).toBeNull();
    });
    expect(await row(iphone.name)).toBeInTheDocument();
  });

  it('closes the dialog after deleting the last passkey and does not bring it back', async () => {
    const list = servePasskeys([macbook]);
    server.use(
      http.delete('/api/me/passkeys/:id', () => {
        list.items = [];
        return new HttpResponse(null, { status: 204 });
      }),
      handlers.passkeyRegisterBegin(),
      http.post('/api/me/passkeys/register/finish', () => {
        list.items = [iphone];
        return HttpResponse.json(iphone);
      }),
    );
    renderProfile();

    const dialog = await openDelete(macbook.name);
    act(() => {
      within(dialog).getByRole('button', { name: 'Удалить' }).click();
    });
    await screen.findByText('Passkey ещё нет');
    await waitFor(() => {
      expect(dialog).not.toHaveAttribute('open');
    });

    add(iphone.name);

    expect(await row(iphone.name)).toBeInTheDocument();
    expect(document.querySelector('dialog[open]')).toBeNull();
  });

  it('keeps the passkey when the dialog is cancelled', async () => {
    let deleted = false;
    server.use(
      handlers.getPasskeys([macbook]),
      http.delete('/api/me/passkeys/:id', () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderProfile();

    const dialog = await openDelete(macbook.name);
    act(() => {
      within(dialog).getByRole('button', { name: 'Отмена' }).click();
    });

    expect(dialog).not.toHaveAttribute('open');
    expect(deleted).toBe(false);
  });

  it('shows the server message in the dialog', async () => {
    const gone = { code: 'passkey_not_found', message: 'Passkey не найден' };
    server.use(handlers.getPasskeys([macbook]), handlers.deletePasskeyError(404, gone));
    renderProfile();

    const dialog = await openDelete(macbook.name);
    act(() => {
      within(dialog).getByRole('button', { name: 'Удалить' }).click();
    });

    expect(await within(dialog).findByText(gone.message)).toHaveClass('notice', 'notice-err');
    expect(dialog).toHaveAttribute('open');
  });
});

it('shows a failed list with a retry', async () => {
  server.use(handlers.getPasskeysError(500, failure));
  renderProfile();

  const card = await passkeyCard();
  const alert = await within(card).findByRole('alert', {}, { timeout: 3000 });
  expect(alert).toHaveTextContent(failure.message);

  server.use(handlers.getPasskeys([macbook]));
  act(() => {
    within(alert).getByRole('button', { name: 'Повторить' }).click();
  });

  expect(await row(macbook.name)).toBeInTheDocument();
});
