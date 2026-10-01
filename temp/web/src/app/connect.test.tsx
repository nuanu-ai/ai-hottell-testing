import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import type { components } from '../shared/api';
import { handlers, issuedMcpKey, noKeys, server } from '../shared/api/test/server';
import { formatAgo, formatDateTime } from '../shared/lib/time';
import { createApp } from './createApp';
import { AppProviders } from './providers';

type KeysStatus = components['schemas']['KeysStatus'];
type KeyStatus = components['schemas']['KeyStatus'];

const failure = { code: 'internal', message: 'Сервис недоступен' };

const activeKey: KeyStatus = {
  active: true,
  createdAt: '2026-09-20T10:15:00Z',
  lastUsedAt: null,
};

const usedKey: KeyStatus = {
  active: true,
  createdAt: '2026-09-21T11:00:00Z',
  lastUsedAt: new Date(Date.now() - 5 * 60_000).toISOString(),
};

const inactive: KeyStatus = { active: false, createdAt: null, lastUsedAt: null };

const claudeCommand =
  'claude mcp add --scope user --transport http hottell http://localhost:8080/mcp \\\n' +
  '  --header "Authorization: Bearer ht_mcp_0123456789abcdef"';

const codexBlock =
  '[mcp_servers.hottell]\n' +
  'url = "http://localhost:8080/mcp"\n' +
  'http_headers = { Authorization = "Bearer ht_mcp_0123456789abcdef" }';

function renderConnect() {
  const app = createApp(createMemoryHistory({ initialEntries: ['/connect'] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

// Keys that answer from `state` and change as the server would, counting each request.
function serveKeys(initial: KeysStatus) {
  const state = { keys: initial, asked: 0, issued: 0, revoked: 0, reissued: 0 };
  server.use(
    http.get('/api/me/keys', () => {
      state.asked += 1;
      return HttpResponse.json(state.keys);
    }),
    http.post('/api/me/keys/mcp', () => {
      state.issued += 1;
      state.keys = { ...state.keys, mcp: { ...activeKey, createdAt: '2026-10-01T09:00:00Z' } };
      return HttpResponse.json(issuedMcpKey, { status: 201 });
    }),
    http.delete('/api/me/keys/mcp', () => {
      state.revoked += 1;
      state.keys = { ...state.keys, mcp: inactive };
      return new HttpResponse(null, { status: 204 });
    }),
    http.post('/api/me/keys/ingest/reissue', () => {
      state.reissued += 1;
      state.keys = { ...state.keys, ingest: { ...activeKey, createdAt: '2026-10-01T09:30:00Z' } };
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return state;
}

async function card(title: string) {
  const heading = await screen.findByRole('heading', { level: 2, name: title });
  const found = heading.closest('.card');
  expect(found).not.toBeNull();
  return found as HTMLElement;
}

const mcpCard = () => card('Ключ MCP');
const collectorCard = () => card('Токен коллектора');

const codeTexts = (root: HTMLElement) =>
  [...root.querySelectorAll('.код code')].map((code) => code.textContent);

function dialog(name: string) {
  return screen.getByRole('dialog', { name, hidden: true });
}

function click(element: HTMLElement) {
  act(() => {
    element.click();
  });
}

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
  server.use(handlers.getMe());
});

it('is in the Settings menu as «Подключение» with its crumbs and title', async () => {
  renderConnect();

  expect(await screen.findByText('Подключение', { selector: 'span.крупно' })).toBeInTheDocument();
  const item = document.querySelector('a.nav-item[href="/connect"]');
  expect(item?.querySelector('.nav-text')).toHaveTextContent('Подключение');
  const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
  expect(crumbs.querySelector('span.crumb-group')).toHaveTextContent('Настройки');
  expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Подключение');
});

it('shows the loading state until the keys arrive', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.get(
      '/api/me/keys',
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderConnect();

  const loading = await screen.findByText('Загружаем ключи…');
  expect(loading).toHaveClass('loading');
  act(() => {
    answer(HttpResponse.json(noKeys));
  });
  expect(await mcpCard()).toBeInTheDocument();
  expect(screen.queryByText('Загружаем ключи…')).toBeNull();
});

it('shows a failed load with a retry that loads the keys', async () => {
  server.use(http.get('/api/me/keys', () => HttpResponse.json(failure, { status: 500 })));
  renderConnect();

  // One retry comes first, a second later.
  const alert = await screen.findByRole('alert', {}, { timeout: 3000 });
  expect(alert).toHaveTextContent('Сервис недоступен');
  serveKeys(noKeys);
  click(within(alert).getByRole('button', { name: 'Повторить' }));
  expect(await mcpCard()).toBeInTheDocument();
});

describe('Ключ MCP', () => {
  it('without a key offers only «Выпустить» and no fragments', async () => {
    serveKeys(noKeys);
    renderConnect();

    const found = await mcpCard();
    expect(within(found).getByText('Ключа ещё нет')).toBeInTheDocument();
    expect(within(found).getByRole('button', { name: 'Выпустить' })).toHaveClass('btn-primary');
    expect(within(found).queryByRole('button', { name: 'Перевыпустить' })).toBeNull();
    expect(within(found).queryByRole('button', { name: 'Отозвать' })).toBeNull();
    expect(codeTexts(found)).toEqual([]);
  });

  it('issues the key and shows it once with the Claude Code and Codex fragments', async () => {
    const state = serveKeys(noKeys);
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Выпустить' }));

    expect(
      await within(found).findByText(/Ключ показан один раз: больше он показан не будет/),
    ).toHaveClass('notice-warn');
    expect(codeTexts(found)).toEqual([issuedMcpKey.key, claudeCommand, codexBlock]);
    expect(within(found).getByRole('heading', { level: 3, name: 'Claude Code' })).toBeVisible();
    expect(within(found).getByRole('heading', { level: 3, name: 'Codex' })).toBeVisible();
    expect(within(found).getByText('~/.codex/config.toml')).toBeInTheDocument();
    // The status is refreshed: the key is active now.
    expect(await within(found).findByRole('button', { name: 'Отозвать' })).toBeInTheDocument();
    expect(state.issued).toBe(1);
  });

  it('never shows the key again once the page is left', async () => {
    serveKeys(noKeys);
    const app = renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Выпустить' }));
    await within(found).findByText(issuedMcpKey.key);

    await act(() => app.router.navigate({ to: '/profile' }));
    await act(() => app.router.navigate({ to: '/connect' }));

    const again = await mcpCard();
    expect(await within(again).findByRole('button', { name: 'Отозвать' })).toBeInTheDocument();
    expect(screen.queryByText(issuedMcpKey.key)).toBeNull();
    expect(codeTexts(again)).toEqual([]);
  });

  it('shows why issuing failed and keeps «Выпустить»', async () => {
    serveKeys(noKeys);
    server.use(http.post('/api/me/keys/mcp', () => HttpResponse.json(failure, { status: 500 })));
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Выпустить' }));

    expect(await within(found).findByRole('alert')).toHaveTextContent('Сервис недоступен');
    expect(within(found).getByRole('button', { name: 'Выпустить' })).toBeEnabled();
    expect(codeTexts(found)).toEqual([]);
  });

  it('with a key shows when it was created and last used, with its two actions', async () => {
    serveKeys({ ...noKeys, mcp: usedKey });
    renderConnect();

    const found = await mcpCard();
    expect(within(found).getByText('активен')).toHaveClass('badge', 'badge-ok');
    expect(found).toHaveTextContent(`создан ${formatDateTime(new Date(usedKey.createdAt ?? ''))}`);
    expect(found).toHaveTextContent(
      `последнее использование ${formatAgo(new Date(usedKey.lastUsedAt ?? ''))}`,
    );
    expect(within(found).getByRole('button', { name: 'Перевыпустить' })).toBeInTheDocument();
    expect(within(found).getByRole('button', { name: 'Отозвать' })).toBeInTheDocument();
    expect(within(found).queryByRole('button', { name: 'Выпустить' })).toBeNull();
  });

  it('says a key that was never used has not been used yet', async () => {
    serveKeys({ ...noKeys, mcp: activeKey });
    renderConnect();

    const found = await mcpCard();
    expect(within(found).getByText('ещё не использовался')).toBeInTheDocument();
    expect(found).not.toHaveTextContent('последнее использование');
  });

  it('reissues only after confirmation and shows the new key once', async () => {
    const state = serveKeys({ ...noKeys, mcp: activeKey });
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить ключ MCP');
    expect(confirm).toHaveAttribute('open');
    expect(confirm).toHaveTextContent('Прежний ключ перестанет работать');
    expect(state.issued).toBe(0);

    click(within(confirm).getByRole('button', { name: 'Перевыпустить' }));

    await waitFor(() => {
      expect(confirm).not.toHaveAttribute('open');
    });
    expect(state.issued).toBe(1);
    expect(await within(found).findByText(issuedMcpKey.key)).toBeInTheDocument();
    expect(codeTexts(found)).toEqual([issuedMcpKey.key, claudeCommand, codexBlock]);
  });

  it('keeps the key when the reissue is cancelled', async () => {
    const state = serveKeys({ ...noKeys, mcp: activeKey });
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить ключ MCP');
    click(within(confirm).getByRole('button', { name: 'Отмена' }));

    expect(confirm).not.toHaveAttribute('open');
    expect(state.issued).toBe(0);
    expect(codeTexts(found)).toEqual([]);
  });

  it('keeps the reissue dialog open with the error when it fails', async () => {
    serveKeys({ ...noKeys, mcp: activeKey });
    server.use(http.post('/api/me/keys/mcp', () => HttpResponse.json(failure, { status: 500 })));
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить ключ MCP');
    click(within(confirm).getByRole('button', { name: 'Перевыпустить' }));

    expect(await within(confirm).findByRole('alert', { hidden: true })).toHaveTextContent(
      'Сервис недоступен',
    );
    expect(confirm).toHaveAttribute('open');
    expect(codeTexts(found)).toEqual([]);
  });

  it('revokes only after confirmation and hides the key just shown', async () => {
    const state = serveKeys(noKeys);
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Выпустить' }));
    click(await within(found).findByRole('button', { name: 'Отозвать' }));
    const confirm = dialog('Отозвать ключ MCP');
    expect(confirm).toHaveTextContent('Агенты и бинарь больше не подключатся');
    expect(state.revoked).toBe(0);

    click(within(confirm).getByRole('button', { name: 'Отозвать' }));

    expect(await within(found).findByText('Ключа ещё нет')).toBeInTheDocument();
    expect(state.revoked).toBe(1);
    expect(codeTexts(found)).toEqual([]);
    expect(confirm).not.toHaveAttribute('open');
  });

  it('keeps the revoke dialog open with the error when it fails', async () => {
    serveKeys({ ...noKeys, mcp: activeKey });
    server.use(
      http.delete('/api/me/keys/mcp', () =>
        HttpResponse.json(
          { code: 'access_key_not_found', message: 'Ключа MCP нет' },
          { status: 404 },
        ),
      ),
    );
    renderConnect();

    const found = await mcpCard();
    click(within(found).getByRole('button', { name: 'Отозвать' }));
    const confirm = dialog('Отозвать ключ MCP');
    click(within(confirm).getByRole('button', { name: 'Отозвать' }));

    expect(await within(confirm).findByRole('alert', { hidden: true })).toHaveTextContent(
      'Ключа MCP нет',
    );
    expect(confirm).toHaveAttribute('open');
  });
});

describe('Токен коллектора', () => {
  it('without a token says the binary will get it and offers no reissue', async () => {
    serveKeys(noKeys);
    renderConnect();

    const found = await collectorCard();
    expect(found).toHaveTextContent(
      'Токена ещё нет: бинарь получит его через MCP при первом подключении',
    );
    expect(within(found).queryByRole('button', { name: 'Перевыпустить' })).toBeNull();
  });

  it('with a token shows its status and never its value', async () => {
    serveKeys({ ...noKeys, ingest: usedKey });
    renderConnect();

    const found = await collectorCard();
    expect(within(found).getByText('активен')).toHaveClass('badge', 'badge-ok');
    expect(found).toHaveTextContent(`создан ${formatDateTime(new Date(usedKey.createdAt ?? ''))}`);
    expect(found).toHaveTextContent(
      `последнее использование ${formatAgo(new Date(usedKey.lastUsedAt ?? ''))}`,
    );
    expect(codeTexts(found)).toEqual([]);
  });

  it('reissues after a confirmation that sending stops on every machine', async () => {
    const state = serveKeys({ ...noKeys, ingest: usedKey });
    renderConnect();

    const found = await collectorCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить токен коллектора');
    expect(confirm).toHaveTextContent(
      'Отправка телеметрии со всех машин остановится, пока бинарь не получит новый токен через MCP.',
    );
    expect(state.reissued).toBe(0);

    click(within(confirm).getByRole('button', { name: 'Перевыпустить' }));

    await waitFor(() => {
      expect(confirm).not.toHaveAttribute('open');
    });
    expect(state.reissued).toBe(1);
    await waitFor(() => {
      expect(found).toHaveTextContent(`создан ${formatDateTime(new Date('2026-10-01T09:30:00Z'))}`);
    });
  });

  it('keeps the token when the reissue is cancelled', async () => {
    const state = serveKeys({ ...noKeys, ingest: usedKey });
    renderConnect();

    const found = await collectorCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить токен коллектора');
    click(within(confirm).getByRole('button', { name: 'Отмена' }));

    expect(confirm).not.toHaveAttribute('open');
    expect(state.reissued).toBe(0);
  });

  it('keeps the reissue dialog open with the error when it fails', async () => {
    serveKeys({ ...noKeys, ingest: usedKey });
    server.use(
      http.post('/api/me/keys/ingest/reissue', () => HttpResponse.json(failure, { status: 500 })),
    );
    renderConnect();

    const found = await collectorCard();
    click(within(found).getByRole('button', { name: 'Перевыпустить' }));
    const confirm = dialog('Перевыпустить токен коллектора');
    click(within(confirm).getByRole('button', { name: 'Перевыпустить' }));

    expect(await within(confirm).findByRole('alert', { hidden: true })).toHaveTextContent(
      'Сервис недоступен',
    );
    expect(confirm).toHaveAttribute('open');
  });
});
