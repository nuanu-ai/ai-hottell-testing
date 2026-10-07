import type { components } from '../../shared/api';
import { plural } from '../../shared/lib/format';

type Schemas = components['schemas'];

export type TelemetrySettings = Schemas['TelemetrySettings'];
export type AgentId = 'claude' | 'codex';
export type Source = keyof Schemas['TelemetrySources'];
export type ContentCategory = Schemas['NativeContentCategory'];
export type HookEvent = Schemas['ClaudeHookEvent'] | Schemas['CodexHookEvent'];
export type FolderList = keyof Schemas['FolderRules'];

/**
 * A mark of the applicability table (settings.md): warn is ⚠, off is ✗ or —. A tag is the
 * note's short name, shown next to the title.
 */
export type Note = { text: string; tone?: 'warn' | 'off'; tag?: string };

// Everything is sent by default: an absent field is an allowed one.
const allSources: Schemas['TelemetrySources'] = {
  hooks: true,
  transcripts: true,
  native_metrics: true,
  native_logs: true,
  native_traces: true,
};

export const agents: { id: AgentId; title: string }[] = [
  { id: 'claude', title: 'Claude Code' },
  { id: 'codex', title: 'Codex' },
];

/** Who sends a source: the binary itself, or the agent's own OpenTelemetry. */
export type SourceGroup = 'binary' | 'native';

export const sourceGroups: { id: SourceGroup; title: string }[] = [
  { id: 'binary', title: 'Через бинарь' },
  { id: 'native', title: 'Нативный OTel агента' },
];

/** A source with where it lives at each agent, settings.md kind 2; a note is optional. */
export type SourceItem = {
  id: Source;
  title: string;
  group: SourceGroup;
  keys: Record<AgentId, string>;
  notes: Partial<Record<AgentId, Note>>;
};

// The keys and notes are settings.md kind 2 and the footnotes of its applicability table.
export const sources: SourceItem[] = [
  {
    id: 'hooks',
    title: 'События хуков',
    group: 'binary',
    keys: { claude: '~/.claude/settings.json → hooks', codex: '~/.codex/hooks.json' },
    notes: {},
  },
  {
    id: 'transcripts',
    title: 'Транскрипты',
    group: 'binary',
    keys: {
      claude: '~/.claude/projects/…/<сессия>.jsonl',
      codex: '~/.codex/sessions/…/rollout-*.jsonl',
    },
    notes: {
      claude: { text: 'И транскрипты субагентов. Без них история не догружается' },
      codex: { text: 'Без них история не догружается' },
    },
  },
  {
    id: 'native_metrics',
    title: 'Нативные метрики',
    group: 'native',
    keys: { claude: 'OTEL_METRICS_EXPORTER', codex: '[otel] metrics_exporter' },
    notes: {
      codex: {
        tone: 'warn',
        tag: 'Statsig',
        text: 'Выключаются явно: без настройки Codex отправляет метрики в Statsig (OpenAI)',
      },
    },
  },
  {
    id: 'native_logs',
    title: 'Нативные события',
    group: 'native',
    keys: { claude: 'OTEL_LOGS_EXPORTER', codex: '[otel] exporter' },
    notes: {},
  },
  {
    id: 'native_traces',
    title: 'Нативные трейсы',
    group: 'native',
    keys: { claude: 'OTEL_TRACES_EXPORTER', codex: '[otel] trace_exporter' },
    notes: { claude: { text: 'Бета-трейсинг Claude Code' } },
  },
];

/** What turning an agent off does, settings.md kind 1. */
export const agentOffEffect = 'хуки не ставятся, транскрипты не читаются, нативный OTel выключен';

/**
 * How a content deny works at an agent: ok — it does; warn — with a limit; off — the agent
 * has the content but no key to cut it; na — the agent has no such content.
 */
export type ContentTone = 'ok' | 'warn' | 'off' | 'na';

/** A category at one agent: the native signals that carry it and how its deny works there. */
export type ContentUse = { signals: Source[]; tone: ContentTone; note?: string };

export type ContentItem = { id: ContentCategory; title: string; use: Record<AgentId, ContentUse> };

export const contentToneTitles: Record<ContentTone, string> = {
  ok: 'действует',
  warn: 'с ограничением',
  off: 'не действует',
  na: 'не относится',
};

// The applicability table of settings.md: the signals are its ✓, ⚠ and ✗ marks; a note is
// its footnote.
export const contentCategories: ContentItem[] = [
  {
    id: 'prompts',
    title: 'Промпты',
    use: {
      claude: { signals: ['native_logs', 'native_traces'], tone: 'ok' },
      codex: { signals: ['native_logs'], tone: 'ok' },
    },
  },
  {
    id: 'assistant_responses',
    title: 'Ответы ассистента',
    use: {
      claude: { signals: ['native_logs'], tone: 'ok' },
      codex: { signals: ['native_logs'], tone: 'ok' },
    },
  },
  {
    id: 'tool_details',
    title: 'Детали инструментов',
    use: {
      claude: {
        signals: ['native_metrics', 'native_logs', 'native_traces'],
        tone: 'warn',
        note: 'Ограничено в метриках — см. «Чего запреты не закрывают»',
      },
      codex: {
        signals: ['native_logs'],
        tone: 'off',
        note: 'У Codex нет такого ключа — см. «Чего запреты не закрывают»',
      },
    },
  },
  {
    id: 'tool_content',
    title: 'Содержимое инструментов',
    use: {
      claude: { signals: ['native_traces'], tone: 'ok' },
      codex: {
        signals: ['native_logs'],
        tone: 'warn',
        note: 'Только урезается — см. «Чего запреты не закрывают»',
      },
    },
  },
  {
    id: 'raw_api_bodies',
    title: 'Сырые тела API',
    use: {
      claude: { signals: ['native_logs'], tone: 'ok' },
      codex: { signals: [], tone: 'na', note: 'У Codex такого содержимого нет' },
    },
  },
];

/** «события и трейсы»: the short names of the signals, the last one after «и». */
export function signalList(ids: Source[]): string {
  const names = ids.map((id) => sourceShort[id]);
  return names.length > 1
    ? `${names.slice(0, -1).join(', ')} и ${names[names.length - 1] ?? ''}`
    : (names[0] ?? '');
}

/**
 * What an agent's denies leave open: the footnotes of the applicability table (settings.md),
 * each said once per agent, on its tab.
 */
export const denyGaps: Record<AgentId, string[]> = {
  claude: [
    'Запрет папки не действует на нативный OTel: сессии Claude Code в запрещённой папке отправляют его по общим настройкам. Чтобы ничего не уходило, выключите источник или агента',
    'Метрики не несут содержимого, но без «Деталей инструментов» настоящие имена агентов, скиллов, плагинов и MCP-серверов в них заменяются обобщёнными — для метрик это не проверено',
  ],
  codex: [
    'Запрет папки не действует на нативный OTel: Codex не читает проектный [otel], и его сессии в запрещённой папке отправляют нативный OTel по общим настройкам. Чтобы ничего не уходило, выключите источник или агента',
    'Аргументы инструментов и межагентные сообщения скрывает только выключение нативных событий: у Codex нет ключа для деталей инструментов',
    'Вывод инструмента Codex только урезает до tool_result.max_bytes, а не убирает гарантированно. Полный вывод инструментов идёт через хуки',
  ],
};

/**
 * When a saved change takes effect at the agent, apply-timing.md: the binary applies the hook
 * and transcript denies itself on every call, so they act at once at both agents; only what
 * it writes into the agent's config — hook entries and native OTel — waits for the agent.
 */
export const applyTiming: Record<AgentId, { what: string; when: string }[]> = {
  claude: [
    { what: 'Хуки и транскрипты: любой запрет, в том числе агента', when: 'сразу' },
    { what: 'Записи хуков в настройках Claude Code', when: 'сразу, в идущей сессии' },
    {
      what: 'Нативный OTel: источники, содержимое и выключение агента',
      when: 'с новой сессии Claude Code',
    },
  ],
  codex: [
    { what: 'Хуки и транскрипты: любой запрет, в том числе агента', when: 'сразу' },
    { what: 'Записи хуков в настройках Codex', when: 'после перезапуска Codex' },
    {
      what: 'Нативный OTel: источники, содержимое и выключение агента',
      when: 'после перезапуска Codex',
    },
  ],
};

/** A deny the agent cannot act on: its switch is locked and keeps the saved value. */
export function isContentLocked(use: ContentUse): boolean {
  return use.tone === 'off' || use.tone === 'na';
}

/**
 * The signals that carry the category and are all turned off, or undefined while one of them
 * is on: such a deny is not needed now.
 */
export function contentSignalsOff(
  settings: TelemetrySettings,
  agent: AgentId,
  use: ContentUse,
): Source[] | undefined {
  if (use.signals.length === 0) return undefined;
  return use.signals.every((id) => !isSourceOn(settings, agent, id)) ? use.signals : undefined;
}

// Each agent's hook events, settings.md kind 3.
export const hookEvents: {
  claude: Schemas['ClaudeHookEvent'][];
  codex: Schemas['CodexHookEvent'][];
} = {
  claude: [
    'SessionStart',
    'Setup',
    'InstructionsLoaded',
    'UserPromptSubmit',
    'UserPromptExpansion',
    'MessageDisplay',
    'PreToolUse',
    'PermissionRequest',
    'PermissionDenied',
    'PostToolUse',
    'PostToolUseFailure',
    'PostToolBatch',
    'Notification',
    'SubagentStart',
    'SubagentStop',
    'TaskCreated',
    'TaskCompleted',
    'Stop',
    'StopFailure',
    'TeammateIdle',
    'ConfigChange',
    'CwdChanged',
    'DirectoryAdded',
    'FileChanged',
    'WorktreeCreate',
    'WorktreeRemove',
    'PreCompact',
    'PostCompact',
    'PreModelSwitch',
    'PostModelSwitch',
    'Elicitation',
    'ElicitationResult',
    'SessionEnd',
  ],
  codex: [
    'SessionStart',
    'SessionEnd',
    'UserPromptSubmit',
    'PreToolUse',
    'PermissionRequest',
    'PostToolUse',
    'PreCompact',
    'PostCompact',
    'SubagentStart',
    'SubagentStop',
    'Stop',
    'Interrupt',
  ],
};

// Events of the schema the binary never hooks: a command hook on WorktreeCreate or
// WorktreeRemove would replace creating or removing the worktree (HT-93).
export const unhookedEvents: Record<AgentId, HookEvent[]> = {
  claude: ['WorktreeCreate', 'WorktreeRemove'],
  codex: [],
};

/** A stage of an agent's work its hook events belong to. */
export type EventGroup =
  'session' | 'prompts' | 'tools' | 'subagents' | 'compact' | 'model' | 'other';

export const eventGroups: { id: EventGroup; title: string }[] = [
  { id: 'session', title: 'Сессия' },
  { id: 'prompts', title: 'Промпты' },
  { id: 'tools', title: 'Инструменты и разрешения' },
  { id: 'subagents', title: 'Субагенты и задачи' },
  { id: 'compact', title: 'Компакт' },
  { id: 'model', title: 'Модель' },
  { id: 'other', title: 'Прочее' },
];

// Every event of the schema, both agents': a new one fails the build until it gets a stage.
const eventGroupOf: Record<HookEvent, EventGroup> = {
  SessionStart: 'session',
  Setup: 'session',
  InstructionsLoaded: 'session',
  Stop: 'session',
  StopFailure: 'session',
  Interrupt: 'session',
  SessionEnd: 'session',
  UserPromptSubmit: 'prompts',
  UserPromptExpansion: 'prompts',
  PreToolUse: 'tools',
  PermissionRequest: 'tools',
  PermissionDenied: 'tools',
  PostToolUse: 'tools',
  PostToolUseFailure: 'tools',
  PostToolBatch: 'tools',
  SubagentStart: 'subagents',
  SubagentStop: 'subagents',
  TaskCreated: 'subagents',
  TaskCompleted: 'subagents',
  TeammateIdle: 'subagents',
  PreCompact: 'compact',
  PostCompact: 'compact',
  MessageDisplay: 'model',
  PreModelSwitch: 'model',
  PostModelSwitch: 'model',
  Notification: 'other',
  ConfigChange: 'other',
  CwdChanged: 'other',
  DirectoryAdded: 'other',
  FileChanged: 'other',
  WorktreeCreate: 'other',
  WorktreeRemove: 'other',
  Elicitation: 'other',
  ElicitationResult: 'other',
};

/**
 * When each hook event comes, for the line under its switch: from the HT-50 studies of
 * Claude Code 2.1.285 and Codex 0.159.0. Typed by the schema, so a new event needs a line.
 */
export const eventNotes: {
  claude: Record<Schemas['ClaudeHookEvent'], string>;
  codex: Record<Schemas['CodexHookEvent'], string>;
} = {
  claude: {
    SessionStart: 'При запуске, возобновлении, очистке или форке сессии',
    Setup: 'При запуске с флагами инициализации или обслуживания',
    InstructionsLoaded: 'Когда загружены CLAUDE.md или файлы правил',
    UserPromptSubmit: 'Когда пользователь отправил промпт, до его обработки',
    UserPromptExpansion: 'Когда разворачивается slash-команда или MCP-промпт',
    MessageDisplay: 'Пока текст ответа ассистента выводится на экран',
    PreToolUse: 'Перед вызовом любого инструмента',
    PermissionRequest: 'Перед показом диалога запроса разрешения на инструмент',
    PermissionDenied: 'Когда авто-режим отклонил вызов инструмента',
    PostToolUse: 'После успешного выполнения инструмента',
    PostToolUseFailure: 'После вызова инструмента, завершившегося ошибкой',
    PostToolBatch: 'После пачки параллельных вызовов инструментов',
    Notification: 'Когда Claude Code показывает уведомление пользователю',
    SubagentStart: 'При запуске или возобновлении субагента',
    SubagentStop: 'Когда субагент закончил работу',
    TaskCreated: 'Когда создана задача в списке задач',
    TaskCompleted: 'Когда задача из списка отмечена выполненной',
    Stop: 'Когда Claude закончил ответ и ход завершён',
    StopFailure: 'Когда ход оборвался из-за ошибки API',
    TeammateIdle: 'Когда тиммейт в команде агентов переходит в простой',
    ConfigChange: 'Когда изменился файл настроек или skills',
    CwdChanged: 'Когда сменилась рабочая директория сессии',
    DirectoryAdded: 'Когда в сессию добавлена рабочая директория',
    FileChanged: 'Когда отслеживаемый файл изменён, создан или удалён',
    WorktreeCreate: 'При создании git worktree',
    WorktreeRemove: 'При удалении git worktree',
    PreCompact: 'Перед ручным или автоматическим сжатием контекста',
    PostCompact: 'После ручного или автоматического сжатия контекста',
    PreModelSwitch: 'Перед сменой модели командой, через пикер или SDK',
    PostModelSwitch: 'После смены модели, в том числе автоматической',
    Elicitation: 'Когда MCP-сервер запрашивает ввод у пользователя',
    ElicitationResult: 'Когда пользователь ответил на запрос MCP-сервера',
    SessionEnd: 'Когда сессия завершается: выход, очистка, logout или resume',
  },
  codex: {
    SessionStart: 'При старте, возобновлении, очистке или форке треда',
    SessionEnd: 'При закрытии сессии, без указания причины',
    UserPromptSubmit: 'Когда пользователь отправил промпт, до его обработки',
    PreToolUse: 'Перед вызовом shell, apply_patch, MCP или другого инструмента',
    PermissionRequest: 'Когда инструменту нужно одобрение, до решения guardian или UI',
    PostToolUse: 'После ответа инструмента, в том числе с ошибкой',
    PreCompact: 'Перед ручным или автоматическим сжатием контекста',
    PostCompact: 'После ручного или автоматического сжатия контекста',
    SubagentStart: 'При запуске нового субагента в треде',
    SubagentStop: 'Когда субагент закончил работу',
    Stop: 'В конце каждого хода агента',
    Interrupt: 'Когда пользователь прервал текущий ход; у Claude Code такого нет',
  },
};

/** The line under an event's switch: when the agent sends it. */
export function eventNote(agent: AgentId, event: HookEvent): string {
  const notes: Partial<Record<HookEvent, string>> = eventNotes[agent];
  return notes[event] ?? '';
}

/** The events the binary hooks for the agent: its list without the never-hooked ones. */
export function hookedEvents(agent: AgentId): HookEvent[] {
  const list: HookEvent[] = hookEvents[agent];
  return list.filter((id) => !unhookedEvents[agent].includes(id));
}

/** The agent's hooked events by stage, in the order of its list; a stage without any is left out. */
export function eventsByGroup(
  agent: AgentId,
): { group: (typeof eventGroups)[number]; events: HookEvent[] }[] {
  const hooked = hookedEvents(agent);
  return eventGroups
    .map((group) => ({ group, events: hooked.filter((id) => eventGroupOf[id] === group.id) }))
    .filter((item) => item.events.length > 0);
}

/** A hook field the page offers to cut, with the events that carry it. */
export type HookFieldItem = { id: string; events: string };

// The content fields of settings.md kind 4, each agent with its own events.
export const hookFields: Record<AgentId, HookFieldItem[]> = {
  claude: [
    { id: 'prompt', events: 'UserPromptSubmit, UserPromptExpansion' },
    {
      id: 'tool_input',
      events: 'PreToolUse, PermissionRequest, PermissionDenied, PostToolUse, PostToolUseFailure',
    },
    { id: 'tool_response', events: 'PostToolUse' },
    { id: 'last_assistant_message', events: 'Stop, SubagentStop, StopFailure' },
    { id: 'compact_summary', events: 'PostCompact' },
    { id: 'custom_instructions', events: 'PreCompact' },
    { id: 'error', events: 'PostToolUseFailure, StopFailure' },
    {
      id: 'tool_calls',
      events: 'PostToolBatch; внутри — tool_input и tool_response пачки',
    },
    { id: 'delta', events: 'MessageDisplay' },
    { id: 'message', events: 'Notification, Elicitation' },
    { id: 'command_args', events: 'UserPromptExpansion' },
    { id: 'content', events: 'ElicitationResult' },
    { id: 'background_tasks', events: 'Stop, SubagentStop' },
    { id: 'session_crons', events: 'Stop, SubagentStop' },
  ],
  codex: [
    { id: 'prompt', events: 'UserPromptSubmit' },
    { id: 'tool_input', events: 'PreToolUse, PermissionRequest, PostToolUse' },
    { id: 'tool_response', events: 'PostToolUse' },
    { id: 'last_assistant_message', events: 'Stop, SubagentStop' },
  ],
};

/** The fields that tie a record to its session: settings.md kind 4 does not let them be cut. */
export const requiredFields = ['session_id', 'hook_event_name', 'cwd'] as const;

/**
 * The denied fields the page does not list for the agent — any top-level name is allowed —
 * in the order they were denied. A required field the binary would not cut anyway is left out.
 */
export function otherDeniedFields(settings: TelemetrySettings, agent: AgentId): string[] {
  const listed = new Set<string>([...hookFields[agent].map((item) => item.id), ...requiredFields]);
  return (settings.agents?.[agent]?.hook_fields?.denied ?? []).filter((id) => !listed.has(id));
}

export function isAgentOn(settings: TelemetrySettings, agent: AgentId): boolean {
  return settings.agents?.[agent]?.enabled ?? true;
}

export function isSourceOn(settings: TelemetrySettings, agent: AgentId, source: Source): boolean {
  return settings.agents?.[agent]?.sources?.[source] ?? true;
}

export function isContentOn(
  settings: TelemetrySettings,
  agent: AgentId,
  category: ContentCategory,
): boolean {
  return !(settings.agents?.[agent]?.native_content?.denied ?? []).includes(category);
}

export function isEventOn(settings: TelemetrySettings, agent: AgentId, event: HookEvent): boolean {
  const denied: HookEvent[] = settings.agents?.[agent]?.hook_events?.denied ?? [];
  return !denied.includes(event);
}

export function isFieldOn(settings: TelemetrySettings, agent: AgentId, field: string): boolean {
  return !(settings.agents?.[agent]?.hook_fields?.denied ?? []).includes(field);
}

type AgentChange = Partial<
  Pick<Schemas['ClaudeTelemetrySettings'], 'enabled' | 'sources' | 'native_content' | 'hook_fields'>
>;

// Every other part of the settings is kept as it was read.
function changeAgent(
  settings: TelemetrySettings,
  agent: AgentId,
  change: AgentChange,
): TelemetrySettings {
  const all = settings.agents ?? {};
  return agent === 'claude'
    ? { ...settings, agents: { ...all, claude: { enabled: true, ...all.claude, ...change } } }
    : { ...settings, agents: { ...all, codex: { enabled: true, ...all.codex, ...change } } };
}

export function setAgent(settings: TelemetrySettings, agent: AgentId, on: boolean) {
  return changeAgent(settings, agent, { enabled: on });
}

export function setSource(
  settings: TelemetrySettings,
  agent: AgentId,
  source: Source,
  on: boolean,
) {
  const current = { ...allSources, ...settings.agents?.[agent]?.sources };
  return changeAgent(settings, agent, { sources: { ...current, [source]: on } });
}

export function setContent(
  settings: TelemetrySettings,
  agent: AgentId,
  category: ContentCategory,
  on: boolean,
) {
  const denied = contentCategories
    .map((item) => item.id)
    .filter((id) => (id === category ? !on : !isContentOn(settings, agent, id)));
  return changeAgent(settings, agent, { native_content: { denied } });
}

export function setBackfill(settings: TelemetrySettings, on: boolean): TelemetrySettings {
  return { ...settings, backfill_history: on };
}

// The agent's denied events, in the order of its list: those `denied` picks.
function withDeniedEvents(
  settings: TelemetrySettings,
  agent: AgentId,
  isDenied: (event: HookEvent) => boolean,
): TelemetrySettings {
  const all = settings.agents ?? {};
  return agent === 'claude'
    ? {
        ...settings,
        agents: {
          ...all,
          claude: {
            enabled: true,
            ...all.claude,
            hook_events: { denied: hookEvents.claude.filter(isDenied) },
          },
        },
      }
    : {
        ...settings,
        agents: {
          ...all,
          codex: {
            enabled: true,
            ...all.codex,
            hook_events: { denied: hookEvents.codex.filter(isDenied) },
          },
        },
      };
}

export function setEvent(
  settings: TelemetrySettings,
  agent: AgentId,
  event: HookEvent,
  on: boolean,
): TelemetrySettings {
  return withDeniedEvents(settings, agent, (id) =>
    id === event ? !on : !isEventOn(settings, agent, id),
  );
}

/**
 * «Все» and «Ничего»: every event the binary hooks on or off. An event it never hooks keeps
 * what it had, so «Ничего» adds no WorktreeCreate or WorktreeRemove to the denies.
 */
export function setEvents(settings: TelemetrySettings, agent: AgentId, on: boolean) {
  return withDeniedEvents(settings, agent, (id) =>
    unhookedEvents[agent].includes(id) ? !isEventOn(settings, agent, id) : !on,
  );
}

/** The events the binary hooks for the agent, and how many of them are sent. */
export function eventCount(settings: TelemetrySettings, agent: AgentId) {
  const hooked = hookedEvents(agent);
  return {
    sent: hooked.filter((id) => isEventOn(settings, agent, id)).length,
    total: hooked.length,
  };
}

// A denied field the page does not list — any top-level name is allowed — stays denied.
export function setField(
  settings: TelemetrySettings,
  agent: AgentId,
  field: string,
  on: boolean,
): TelemetrySettings {
  const current = settings.agents?.[agent]?.hook_fields?.denied ?? [];
  const denied = on
    ? current.filter((id) => id !== field)
    : current.includes(field)
      ? current
      : [...current, field];
  return changeAgent(settings, agent, { hook_fields: { denied } });
}

export function folderPatterns(settings: TelemetrySettings, list: FolderList): string[] {
  return settings.folders?.[list] ?? [];
}

/**
 * Why a folder pattern cannot be added to the list, or undefined when it can: settings.md
 * kind 5 — a path from / or ~/, where ** stands for whole path segments.
 */
export function folderPatternError(pattern: string, existing: string[]): string | undefined {
  if (pattern === '') {
    return 'Введите шаблон пути';
  }
  if (!pattern.startsWith('/') && !pattern.startsWith('~/')) {
    return 'Шаблон начинается с / или с ~/ — домашнего каталога';
  }
  if (pattern.split('/').some((segment) => segment.includes('**') && segment !== '**')) {
    return '** заменяет целые сегменты пути и стоит между /, например ~/work/**';
  }
  if (existing.includes(pattern)) {
    return 'Такой шаблон уже есть в списке';
  }
  return undefined;
}

// The segments of a pattern; a trailing / does not count (settings.md kind 5).
function patternSegments(pattern: string): string[] {
  return pattern.replace(/\/+$/, '').split('/');
}

// Whether an allowed pattern may match a folder the denied pattern closes. Only the literal
// prefix of the deny up to its first ** is compared; anything unclear counts as a yes.
function mayLieUnder(allowed: string[], denied: string[]): boolean {
  // ~ and / roots meet in the home folder, which the page does not know.
  if (allowed[0] !== denied[0]) {
    return true;
  }
  for (const [index, segment] of denied.entries()) {
    if (segment === '**') {
      return true;
    }
    const own = allowed[index];
    if (own === undefined) {
      return false;
    }
    if (own === '**') {
      return true;
    }
    if (own !== segment) {
      return false;
    }
  }
  // The deny names one folder: only a ** tail of the allow still matches it.
  return allowed.slice(denied.length).every((segment) => segment === '**');
}

/**
 * Whether an allowed pattern lies under no denied pattern and so changes nothing: settings.md
 * kind 5. A simple prefix check; when in doubt the pattern is taken as one that changes something.
 */
export function allowChangesNothing(pattern: string, denied: string[]): boolean {
  const allowed = patternSegments(pattern);
  return !denied.some((item) => mayLieUnder(allowed, patternSegments(item)));
}

function changeFolders(
  settings: TelemetrySettings,
  list: FolderList,
  patterns: string[],
): TelemetrySettings {
  const folders = {
    denied: folderPatterns(settings, 'denied'),
    allowed: folderPatterns(settings, 'allowed'),
  };
  return { ...settings, folders: { ...folders, [list]: patterns } };
}

export function addFolder(settings: TelemetrySettings, list: FolderList, pattern: string) {
  return changeFolders(settings, list, [...folderPatterns(settings, list), pattern]);
}

export function removeFolder(settings: TelemetrySettings, list: FolderList, pattern: string) {
  return changeFolders(
    settings,
    list,
    folderPatterns(settings, list).filter((item) => item !== pattern),
  );
}

/** Where a change belongs: an agent's tab or the folders and history shared by both. */
export type Scope = AgentId | 'shared';

/**
 * One point of the settings changed by a draft, with its new value. A change is data, so it
 * can be laid over other settings: the fresh ones after a version conflict.
 */
export type Change = { scope: Scope; text: string } & (
  | { kind: 'agent'; agent: AgentId; on: boolean }
  | { kind: 'source'; agent: AgentId; id: Source; on: boolean }
  | { kind: 'content'; agent: AgentId; id: ContentCategory; on: boolean }
  | { kind: 'event'; agent: AgentId; id: HookEvent; on: boolean }
  | { kind: 'field'; agent: AgentId; id: string; on: boolean }
  | { kind: 'folder'; list: FolderList; pattern: string; on: boolean }
  | { kind: 'backfill'; on: boolean }
);

type Point = Change extends infer C
  ? C extends Change
    ? Omit<C, 'scope' | 'text'>
    : never
  : never;

const agentTitle = (agent: AgentId) => (agent === 'claude' ? 'Claude Code' : 'Codex');

const lowerFirst = (text: string) => text.charAt(0).toLowerCase() + text.slice(1);

function describe(point: Point): Change {
  switch (point.kind) {
    case 'agent':
      return {
        ...point,
        scope: point.agent,
        text: `${agentTitle(point.agent)}: агент ${point.on ? 'включён' : 'выключен'}`,
      };
    case 'source': {
      const title = sources.find((item) => item.id === point.id)?.title ?? point.id;
      return {
        ...point,
        scope: point.agent,
        text: `${agentTitle(point.agent)}: ${lowerFirst(title)} ${point.on ? 'включены' : 'выключены'}`,
      };
    }
    case 'content': {
      const title = contentCategories.find((item) => item.id === point.id)?.title ?? point.id;
      return {
        ...point,
        scope: point.agent,
        text: `${agentTitle(point.agent)}: ${point.on ? 'разрешено' : 'запрещено'} содержимое «${title}»`,
      };
    }
    case 'event':
      return {
        ...point,
        scope: point.agent,
        text: `${agentTitle(point.agent)}: ${point.on ? 'разрешено' : 'запрещено'} событие ${point.id}`,
      };
    case 'field':
      return {
        ...point,
        scope: point.agent,
        text: `${agentTitle(point.agent)}: ${point.on ? 'разрешено' : 'запрещено'} поле ${point.id}`,
      };
    case 'folder': {
      // The denied list holds the folders; the allowed list, the exceptions inside them.
      const what =
        point.list === 'denied'
          ? `${point.on ? 'добавлен' : 'убран'} ${point.pattern}`
          : `${point.on ? 'добавлено' : 'убрано'} исключение ${point.pattern}`;
      return { ...point, scope: 'shared', text: `Папки: ${what}` };
    }
    case 'backfill':
      return {
        ...point,
        scope: 'shared',
        text: `История: ${point.on ? 'догрузить при включении' : 'не догружать'}`,
      };
  }
}

function key(point: Point): string {
  switch (point.kind) {
    case 'agent':
      return `${point.agent}:agent`;
    case 'source':
    case 'content':
    case 'event':
    case 'field':
      return `${point.agent}:${point.kind}:${point.id}`;
    case 'folder':
      return `folder:${point.list}:${point.pattern}`;
    case 'backfill':
      return 'backfill';
  }
}

function deniedFields(settings: TelemetrySettings, agent: AgentId): string[] {
  return settings.agents?.[agent]?.hook_fields?.denied ?? [];
}

const unique = <T>(items: T[]) => [...new Set(items)];

/**
 * The points the draft changed against the base, in the page's order: Claude Code, Codex,
 * then folders and history. An absent field reads as its default, so a draft that only
 * spells out a default changes nothing. The version is the server's and is not a change.
 */
export function changes(base: TelemetrySettings, draft: TelemetrySettings): Change[] {
  const points: Point[] = [];
  for (const { id: agent } of agents) {
    if (isAgentOn(base, agent) !== isAgentOn(draft, agent)) {
      points.push({ kind: 'agent', agent, on: isAgentOn(draft, agent) });
    }
    for (const { id } of sources) {
      const on = isSourceOn(draft, agent, id);
      if (isSourceOn(base, agent, id) !== on) points.push({ kind: 'source', agent, id, on });
    }
    for (const { id } of contentCategories) {
      const on = isContentOn(draft, agent, id);
      if (isContentOn(base, agent, id) !== on) points.push({ kind: 'content', agent, id, on });
    }
    for (const id of hookEvents[agent]) {
      const on = isEventOn(draft, agent, id);
      if (isEventOn(base, agent, id) !== on) points.push({ kind: 'event', agent, id, on });
    }
    // The page's fields first, then any other denied top-level name.
    const fields = unique([
      ...hookFields[agent].map((item) => item.id),
      ...deniedFields(base, agent),
      ...deniedFields(draft, agent),
    ]);
    for (const id of fields) {
      const on = isFieldOn(draft, agent, id);
      if (isFieldOn(base, agent, id) !== on) points.push({ kind: 'field', agent, id, on });
    }
  }
  for (const list of ['denied', 'allowed'] as const) {
    const before = folderPatterns(base, list);
    const after = folderPatterns(draft, list);
    for (const pattern of after.filter((item) => !before.includes(item))) {
      points.push({ kind: 'folder', list, pattern, on: true });
    }
    for (const pattern of before.filter((item) => !after.includes(item))) {
      points.push({ kind: 'folder', list, pattern, on: false });
    }
  }
  if (base.backfill_history !== draft.backfill_history) {
    points.push({ kind: 'backfill', on: draft.backfill_history });
  }
  return points.map(describe);
}

function applyChange(settings: TelemetrySettings, change: Change): TelemetrySettings {
  switch (change.kind) {
    case 'agent':
      return setAgent(settings, change.agent, change.on);
    case 'source':
      return setSource(settings, change.agent, change.id, change.on);
    case 'content':
      return setContent(settings, change.agent, change.id, change.on);
    case 'event':
      return setEvent(settings, change.agent, change.id, change.on);
    case 'field':
      return setField(settings, change.agent, change.id, change.on);
    case 'folder': {
      const has = folderPatterns(settings, change.list).includes(change.pattern);
      if (has === change.on) return settings;
      return change.on
        ? addFolder(settings, change.list, change.pattern)
        : removeFolder(settings, change.list, change.pattern);
    }
    case 'backfill':
      return setBackfill(settings, change.on);
  }
}

/** Lays the changes over other settings, keeping their version: the local change wins. */
export function applyChanges(settings: TelemetrySettings, list: Change[]): TelemetrySettings {
  return list.reduce(applyChange, settings);
}

/** The local changes whose point the fresh settings changed too, whatever its new value. */
export function conflicts(
  base: TelemetrySettings,
  fresh: TelemetrySettings,
  list: Change[],
): Change[] {
  const theirs = new Set(changes(base, fresh).map(key));
  return list.filter((change) => theirs.has(key(change)));
}

/**
 * The denies of a tab: for an agent, the agent itself when off and every source, content
 * category, event and field it denies; for the shared tab, the denied folders. The allowed
 * folders are exceptions, not denies.
 */
export function denialCount(settings: TelemetrySettings, scope: Scope): number {
  if (scope === 'shared') {
    return folderPatterns(settings, 'denied').length;
  }
  const agent = settings.agents?.[scope];
  return (
    (isAgentOn(settings, scope) ? 0 : 1) +
    sources.filter(({ id }) => !isSourceOn(settings, scope, id)).length +
    (agent?.native_content?.denied ?? []).length +
    (agent?.hook_events?.denied ?? []).length +
    deniedFields(settings, scope).length
  );
}

const sourceShort: Record<Source, string> = {
  hooks: 'хуки',
  transcripts: 'транскрипты',
  native_metrics: 'метрики',
  native_logs: 'события',
  native_traces: 'трейсы',
};

export type Summary = {
  /** The agents that send, and the ones turned off. */
  on: AgentId[];
  off: AgentId[];
  /** The sources sent, one marked «только …» when only some of the agents send it. */
  sent: string[];
  /** The sources an agent that sends does not, then the counts of the finer denies. */
  denied: string[];
  /** One line: «Отправляется: Claude Code и Codex · хуки, … · запрещено: 2 папки». */
  text: string;
};

/** What the settings send, for the line above the tabs; an agent that is off adds no denies. */
export function summary(settings: TelemetrySettings): Summary {
  const on = agents.map((item) => item.id).filter((agent) => isAgentOn(settings, agent));
  const off = agents.map((item) => item.id).filter((agent) => !isAgentOn(settings, agent));
  const names = (list: AgentId[]) => list.map(agentTitle).join(' и ');

  const sent: string[] = [];
  const denied: string[] = [];
  for (const { id } of sources) {
    const sending = on.filter((agent) => isSourceOn(settings, agent, id));
    if (sending.length === 0) continue;
    if (sending.length === on.length) {
      sent.push(sourceShort[id]);
    } else {
      sent.push(`${sourceShort[id]} (только ${names(sending)})`);
    }
  }
  for (const { id } of sources) {
    for (const agent of on.filter((item) => !isSourceOn(settings, item, id))) {
      denied.push(`${sourceShort[id]} ${agentTitle(agent)}`);
    }
  }
  const count = (n: number, one: string, few: string, many: string) => {
    if (n > 0) denied.push(`${String(n)} ${plural(n, one, few, many)}`);
  };
  const total = (part: (agent: AgentId) => number) =>
    on.reduce((sum, agent) => sum + part(agent), 0);
  const settingsOf = (agent: AgentId) => settings.agents?.[agent];
  count(
    total((agent) => settingsOf(agent)?.hook_events?.denied.length ?? 0),
    'событие',
    'события',
    'событий',
  );
  count(
    total((agent) => deniedFields(settings, agent).length),
    'поле',
    'поля',
    'полей',
  );
  count(
    total((agent) => settingsOf(agent)?.native_content?.denied.length ?? 0),
    'вид содержимого',
    'вида содержимого',
    'видов содержимого',
  );
  count(denialCount(settings, 'shared'), 'папка', 'папки', 'папок');

  let text: string;
  if (on.length === 0) {
    text = `Ничего не отправляется: ${names(off)} ${off.length > 1 ? 'выключены' : 'выключен'}`;
  } else if (off.length === 0 && denied.length === 0) {
    text = 'Отправляется всё';
  } else {
    text = [
      `Отправляется: ${names(on)}`,
      sent.length > 0 ? sent.join(', ') : 'источники выключены',
      ...off.map((agent) => `${agentTitle(agent)} выключен`),
      ...(denied.length > 0 ? [`запрещено: ${denied.join(', ')}`] : []),
    ].join(' · ');
  }
  return { on, off, sent, denied, text };
}
