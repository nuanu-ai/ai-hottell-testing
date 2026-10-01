import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import type { components } from '../shared/api';
import { handlers, server } from '../shared/api/test/server';
import { createApp } from './createApp';
import { AppProviders } from './providers';

type Schemas = components['schemas'];
type VersionedSettings = Schemas['VersionedTelemetrySettings'];
type UpdateRequest = Schemas['UpdateTelemetrySettingsRequest'];

const allowAll = {
  enabled: true,
  sources: {
    hooks: true,
    transcripts: true,
    native_metrics: true,
    native_logs: true,
    native_traces: true,
  },
  hook_events: { denied: [] },
  hook_fields: { denied: [] },
  native_content: { denied: [] },
};

// The full form the server answers, with a deny of part 2 that the page must keep as is.
const stored: VersionedSettings = {
  version: 3,
  settings: {
    version: 0,
    agents: {
      claude: { ...allowAll, hook_events: { denied: ['MessageDisplay'] } },
      codex: { ...allowAll, native_content: { denied: ['prompts'] } },
    },
    folders: { denied: ['~/work/**'], allowed: [] },
    backfill_history: false,
  },
};

function renderSettings() {
  const app = createApp(createMemoryHistory({ initialEntries: ['/telemetry'] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

// Settings that answer from `state` and save as the server would: in place of the version
// they were read at, one version up.
function serveSettings(initial: VersionedSettings) {
  const state = { current: initial, saved: [] as UpdateRequest[] };
  server.use(
    http.get('/api/me/telemetry-settings', () => HttpResponse.json(state.current)),
    http.put('/api/me/telemetry-settings', async ({ request }) => {
      const body = (await request.json()) as UpdateRequest;
      state.saved.push(body);
      if (body.expectedVersion !== state.current.version) {
        return HttpResponse.json(
          { code: 'settings_version_conflict', message: 'Настройки изменились' },
          { status: 409 },
        );
      }
      state.current = { version: state.current.version + 1, settings: body.settings };
      return HttpResponse.json(state.current);
    }),
  );
  return state;
}

async function agentCard(title: string) {
  const heading = await screen.findByRole('heading', { level: 2, name: title });
  return heading.closest('.card') as HTMLElement;
}

function click(element: HTMLElement) {
  act(() => {
    element.click();
  });
}

const save = () => screen.getByRole('button', { name: 'Сохранить' });

beforeEach(() => {
  server.use(handlers.getMe());
});

it('is in the Settings menu as «Что отправлять» with its crumbs and title', async () => {
  serveSettings(stored);
  renderSettings();

  expect(
    await screen.findByText('Что отправлять', { selector: 'span.крупно' }),
  ).toBeInTheDocument();
  const item = document.querySelector('a.nav-item[href="/telemetry"]');
  expect(item?.querySelector('.nav-text')).toHaveTextContent('Что отправлять');
  const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
  expect(crumbs.querySelector('span.crumb-group')).toHaveTextContent('Настройки');
  expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Что отправлять');
});

it('loads the settings into the switches with the applicability notes', async () => {
  let answer: (response: Response) => void = () => undefined;
  server.use(
    http.get(
      '/api/me/telemetry-settings',
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  renderSettings();

  expect(await screen.findByText('Загружаем настройки…')).toHaveClass('loading');
  act(() => {
    answer(HttpResponse.json(stored));
  });

  const claude = await agentCard('Claude Code');
  const codex = await agentCard('Codex');
  for (const card of [claude, codex]) {
    for (const name of [
      'События хуков',
      'Транскрипты',
      'Нативные метрики',
      'Нативные события',
      'Нативные трейсы',
      'Ответы ассистента',
      'Детали инструментов',
      'Содержимое инструментов',
      'Сырые тела API',
    ]) {
      expect(within(card).getByRole('checkbox', { name })).toBeChecked();
    }
  }
  expect(
    within(claude).getByRole('checkbox', { name: 'Отправлять данные Claude Code' }),
  ).toBeChecked();
  expect(within(claude).getByRole('checkbox', { name: 'Промпты' })).toBeChecked();
  expect(within(codex).getByRole('checkbox', { name: 'Промпты' })).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Догрузить историю' })).not.toBeChecked();
  expect(screen.getByText('Версия настроек: 3')).toBeInTheDocument();

  // The notes of the applicability table differ by agent.
  expect(
    within(codex).getByRole('checkbox', { name: 'Содержимое инструментов' }),
  ).toHaveAccessibleDescription(/^⚠ Ограничено нативно: Codex только урезает вывод инструмента/);
  expect(
    within(claude).getByRole('checkbox', { name: 'Содержимое инструментов' }),
  ).toHaveAccessibleDescription('В нативных трейсах');
  expect(
    within(codex).getByRole('checkbox', { name: 'Сырые тела API' }),
  ).toHaveAccessibleDescription('✗ Не относится: у Codex такого содержимого нет');
  expect(
    within(codex).getByRole('checkbox', { name: 'Детали инструментов' }),
  ).toHaveAccessibleDescription(/^✗ Не действует: у Codex нет такого ключа/);
  expect(
    within(claude).getByText(/Запрет папки на нативный OTel не действует/),
  ).toBeInTheDocument();
});

it('shows a failed load with a retry', async () => {
  server.use(
    http.get('/api/me/telemetry-settings', () =>
      HttpResponse.json({ code: 'internal', message: 'Сервис недоступен' }, { status: 500 }),
    ),
  );
  renderSettings();

  const alert = await screen.findByRole('alert', {}, { timeout: 3000 });
  expect(alert).toHaveTextContent('Сервис недоступен');
  serveSettings(stored);
  click(within(alert).getByRole('button', { name: 'Повторить' }));
  expect(await agentCard('Claude Code')).toBeInTheDocument();
});

it('saves the changes at the version read and keeps the rest of the settings', async () => {
  const state = serveSettings(stored);
  renderSettings();

  const claude = await agentCard('Claude Code');
  const codex = await agentCard('Codex');
  click(within(claude).getByRole('checkbox', { name: 'Транскрипты' }));
  click(within(claude).getByRole('checkbox', { name: 'Сырые тела API' }));
  click(within(codex).getByRole('checkbox', { name: 'Промпты' }));
  click(within(codex).getByRole('checkbox', { name: 'Отправлять данные Codex' }));
  click(screen.getByRole('checkbox', { name: 'Догрузить историю' }));
  click(save());

  expect(
    await screen.findByText(
      /Сохранено\. Изменения нативного OTel вступят в силу в новых сессиях агентов/,
    ),
  ).toHaveClass('notice-ok');
  expect(screen.getByText('Версия настроек: 4')).toBeInTheDocument();
  expect(state.saved).toEqual([
    {
      expectedVersion: 3,
      settings: {
        ...stored.settings,
        agents: {
          claude: {
            ...allowAll,
            hook_events: { denied: ['MessageDisplay'] },
            sources: { ...allowAll.sources, transcripts: false },
            native_content: { denied: ['raw_api_bodies'] },
          },
          codex: { ...allowAll, enabled: false },
        },
        backfill_history: true,
      },
    },
  ]);

  // A second save goes at the new version, and a change hides the old «Сохранено».
  click(within(claude).getByRole('checkbox', { name: 'Нативные трейсы' }));
  expect(screen.queryByText(/Сохранено/)).toBeNull();
  click(save());
  expect(await screen.findByText('Версия настроек: 5')).toBeInTheDocument();
  expect(state.saved[1]?.expectedVersion).toBe(4);
});

it('turning an agent off disables its sources and categories', async () => {
  serveSettings(stored);
  renderSettings();

  const codex = await agentCard('Codex');
  click(within(codex).getByRole('checkbox', { name: 'Отправлять данные Codex' }));

  expect(within(codex).getByRole('checkbox', { name: 'Транскрипты' })).toBeDisabled();
  expect(within(codex).getByRole('checkbox', { name: 'Промпты' })).toBeDisabled();
  expect(
    within(await agentCard('Claude Code')).getByRole('checkbox', { name: 'Транскрипты' }),
  ).toBeEnabled();
});

it('on 409 says the settings changed elsewhere and reloads them', async () => {
  const state = serveSettings(stored);
  renderSettings();

  const claude = await agentCard('Claude Code');
  // Another window saves in between.
  state.current = {
    version: 4,
    settings: { ...stored.settings, backfill_history: true },
  };
  click(within(claude).getByRole('checkbox', { name: 'Нативные метрики' }));
  click(save());

  const notice = await screen.findByText(/Настройки изменились в другом окне/);
  expect(notice).toHaveClass('notice-warn');
  expect(state.saved.map((body) => body.expectedVersion)).toEqual([3]);

  click(within(notice).getByRole('button', { name: 'Перезагрузить' }));
  expect(await screen.findByText('Версия настроек: 4')).toBeInTheDocument();
  expect(screen.queryByText(/Настройки изменились в другом окне/)).toBeNull();
  expect(screen.getByRole('checkbox', { name: 'Догрузить историю' })).toBeChecked();
  expect(within(claude).getByRole('checkbox', { name: 'Нативные метрики' })).toBeChecked();

  click(save());
  await waitFor(() => {
    expect(state.saved.map((body) => body.expectedVersion)).toEqual([3, 4]);
  });
  expect(await screen.findByText('Версия настроек: 5')).toBeInTheDocument();
});

it('on 422 shows the error with where and why', async () => {
  serveSettings(stored);
  server.use(
    http.put('/api/me/telemetry-settings', () =>
      HttpResponse.json(
        {
          code: 'invalid_telemetry_settings',
          message: 'Настройки не подходят под схему',
          detail: 'agents.claude.hook_events.denied[0] unknown hook event',
        },
        { status: 422 },
      ),
    ),
  );
  renderSettings();

  await agentCard('Claude Code');
  click(save());

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Настройки не подходят под схему: agents.claude.hook_events.denied[0] unknown hook event',
  );
  expect(screen.getByText('Версия настроек: 3')).toBeInTheDocument();
});

async function folderList(name: string) {
  const card = await agentCard('Папки проектов');
  return within(card).getByRole('region', { name });
}

function addPattern(list: HTMLElement, pattern: string) {
  const input = within(list).getByRole('textbox', { name: 'Шаблон пути' });
  act(() => {
    fireEvent.change(input, { target: { value: pattern } });
  });
  click(within(list).getByRole('button', { name: 'Добавить' }));
}

it('lists each agent its own hook events and fields with the events of a field', async () => {
  serveSettings(stored);
  renderSettings();

  const claude = await agentCard('Claude Code');
  const codex = await agentCard('Codex');
  const events = (name: string) =>
    within(screen.getByRole('group', { name: `События хуков ${name}` })).getAllByRole('checkbox');
  expect(events('Claude Code')).toHaveLength(33);
  expect(events('Codex')).toHaveLength(12);
  expect(within(claude).getByRole('checkbox', { name: 'MessageDisplay' })).not.toBeChecked();
  expect(within(claude).getByRole('checkbox', { name: 'SessionStart' })).toBeChecked();
  expect(within(codex).getByRole('checkbox', { name: 'Interrupt' })).toBeChecked();
  expect(within(codex).queryByRole('checkbox', { name: 'MessageDisplay' })).toBeNull();
  // The binary never hooks the worktree events of Claude Code.
  for (const name of ['WorktreeCreate', 'WorktreeRemove']) {
    const box = within(claude).getByRole('checkbox', { name });
    expect(box).not.toBeChecked();
    expect(box).toBeDisabled();
  }
  expect(
    within(claude).getByText(/WorktreeCreate и WorktreeRemove не отправляются никогда/),
  ).toBeInTheDocument();

  expect(within(claude).getByRole('checkbox', { name: 'prompt' })).toHaveAccessibleDescription(
    'В событиях: UserPromptSubmit, UserPromptExpansion',
  );
  expect(within(codex).getByRole('checkbox', { name: 'prompt' })).toHaveAccessibleDescription(
    'В событиях: UserPromptSubmit',
  );
  expect(within(claude).getByRole('checkbox', { name: 'compact_summary' })).toBeChecked();
  expect(within(codex).queryByRole('checkbox', { name: 'compact_summary' })).toBeNull();
});

it('turning the hooks source off disables its events and fields', async () => {
  serveSettings(stored);
  renderSettings();

  const codex = await agentCard('Codex');
  click(within(codex).getByRole('checkbox', { name: 'События хуков' }));

  expect(within(codex).getByRole('checkbox', { name: 'Interrupt' })).toBeDisabled();
  expect(within(codex).getByRole('checkbox', { name: 'tool_input' })).toBeDisabled();
  expect(
    within(codex).getByText('Источник «События хуков» выключен: не отправляется ни одно событие'),
  ).toBeInTheDocument();
  expect(within(codex).getByRole('checkbox', { name: 'Транскрипты' })).toBeEnabled();
  expect(
    within(await agentCard('Claude Code')).getByRole('checkbox', { name: 'SessionStart' }),
  ).toBeEnabled();
});

it('adds and removes folder patterns', async () => {
  serveSettings(stored);
  renderSettings();

  const denied = await folderList('Не отправлять');
  const allowed = await folderList('Разрешить внутри запрещённого');
  expect(within(denied).getByText('~/work/**')).toBeInTheDocument();

  addPattern(denied, '  /Volumes/secret/**  ');
  addPattern(allowed, '~/work/oss/**');
  expect(within(denied).getByText('/Volumes/secret/**')).toBeInTheDocument();
  expect(within(allowed).getByText('~/work/oss/**')).toBeInTheDocument();
  expect(within(denied).getByRole('textbox', { name: 'Шаблон пути' })).toHaveValue('');

  click(within(denied).getByRole('button', { name: 'Удалить ~/work/**' }));
  expect(within(denied).queryByText('~/work/**')).toBeNull();

  // Enter in the pattern adds it and does not save the form.
  const input = within(allowed).getByRole('textbox', { name: 'Шаблон пути' });
  act(() => {
    fireEvent.change(input, { target: { value: '/tmp/ok' } });
  });
  act(() => {
    fireEvent.keyDown(input, { key: 'Enter' });
  });
  expect(within(allowed).getByText('/tmp/ok')).toBeInTheDocument();
  expect(screen.getByText('Версия настроек: 3')).toBeInTheDocument();
});

it.each([
  ['', 'Введите шаблон пути'],
  ['work/**', 'Шаблон начинается с / или с ~/ — домашнего каталога'],
  ['~work/**', 'Шаблон начинается с / или с ~/ — домашнего каталога'],
  ['~/work**', '** заменяет целые сегменты пути и стоит между /, например ~/work/**'],
  ['/a/**b/c', '** заменяет целые сегменты пути и стоит между /, например ~/work/**'],
  ['~/work/**', 'Такой шаблон уже есть в списке'],
])('refuses the folder pattern %j', async (pattern, message) => {
  serveSettings(stored);
  renderSettings();

  const denied = await folderList('Не отправлять');
  addPattern(denied, pattern);

  const input = within(denied).getByRole('textbox', { name: 'Шаблон пути' });
  expect(input).toHaveAttribute('aria-invalid', 'true');
  expect(input).toHaveAccessibleDescription(message);
  expect(within(denied).getAllByRole('listitem')).toHaveLength(1);

  // Typing clears the error.
  act(() => {
    fireEvent.change(input, { target: { value: '/x' } });
  });
  expect(input).not.toHaveAttribute('aria-invalid');
});

it('saves hook events, fields and folders as the expected document', async () => {
  const state = serveSettings({
    ...stored,
    settings: {
      ...stored.settings,
      agents: {
        ...stored.settings.agents,
        // A field the page does not list stays denied.
        codex: { ...allowAll, hook_fields: { denied: ['custom_field'] } },
      },
    },
  });
  renderSettings();

  const claude = await agentCard('Claude Code');
  const codex = await agentCard('Codex');
  click(within(claude).getByRole('checkbox', { name: 'MessageDisplay' }));
  click(within(claude).getByRole('checkbox', { name: 'SessionEnd' }));
  click(within(claude).getByRole('checkbox', { name: 'FileChanged' }));
  click(within(claude).getByRole('checkbox', { name: 'tool_response' }));
  click(within(claude).getByRole('checkbox', { name: 'compact_summary' }));
  click(within(codex).getByRole('checkbox', { name: 'Interrupt' }));
  click(within(codex).getByRole('checkbox', { name: 'prompt' }));
  addPattern(await folderList('Не отправлять'), '/Volumes/secret/**');
  addPattern(await folderList('Разрешить внутри запрещённого'), '~/work/oss/**');
  click(save());

  expect(await screen.findByText('Версия настроек: 4')).toBeInTheDocument();
  expect(state.saved).toEqual([
    {
      expectedVersion: 3,
      settings: {
        version: 0,
        agents: {
          claude: {
            ...allowAll,
            hook_events: { denied: ['FileChanged', 'SessionEnd'] },
            hook_fields: { denied: ['tool_response', 'compact_summary'] },
          },
          codex: {
            ...allowAll,
            hook_events: { denied: ['Interrupt'] },
            hook_fields: { denied: ['custom_field', 'prompt'] },
          },
        },
        folders: { denied: ['~/work/**', '/Volumes/secret/**'], allowed: ['~/work/oss/**'] },
        backfill_history: false,
      },
    },
  ]);
  expect(within(claude).getByRole('checkbox', { name: 'tool_response' })).not.toBeChecked();
});
