import type { components } from '../../shared/api';

type Schemas = components['schemas'];

export type TelemetrySettings = Schemas['TelemetrySettings'];
export type AgentId = 'claude' | 'codex';
export type Source = keyof Schemas['TelemetrySources'];
export type ContentCategory = Schemas['NativeContentCategory'];
export type HookEvent = Schemas['ClaudeHookEvent'] | Schemas['CodexHookEvent'];
export type FolderList = keyof Schemas['FolderRules'];

/** A mark of the applicability table (settings.md): warn is ⚠, off is ✗ or —. */
export type Note = { text: string; tone?: 'warn' | 'off' };

type Switch<Id> = { id: Id; title: string; notes: Record<AgentId, Note> };

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

// The notes are the applicability table and its footnotes, docs/specs/hottell-contract/settings.md.
export const sources: Switch<Source>[] = [
  {
    id: 'hooks',
    title: 'События хуков',
    notes: {
      claude: { text: 'События, которые бинарь получает как хук' },
      codex: { text: 'События, которые бинарь получает как хук' },
    },
  },
  {
    id: 'transcripts',
    title: 'Транскрипты',
    notes: {
      claude: {
        text: '~/.claude/projects/…/<сессия>.jsonl и транскрипты субагентов. Без них история не догружается',
      },
      codex: { text: 'rollout-*.jsonl из ~/.codex/sessions. Без них история не догружается' },
    },
  },
  {
    id: 'native_metrics',
    title: 'Нативные метрики',
    notes: {
      claude: { text: 'OTEL_METRICS_EXPORTER' },
      codex: {
        text: '[otel] metrics_exporter. Выключаются явно: без настройки Codex отправляет метрики в Statsig (OpenAI)',
      },
    },
  },
  {
    id: 'native_logs',
    title: 'Нативные события',
    notes: {
      claude: { text: 'OTEL_LOGS_EXPORTER' },
      codex: {
        text: '[otel] exporter. Только этот запрет скрывает аргументы инструментов и межагентные сообщения',
      },
    },
  },
  {
    id: 'native_traces',
    title: 'Нативные трейсы',
    notes: {
      claude: { text: 'OTEL_TRACES_EXPORTER, бета-трейсинг Claude Code' },
      codex: { text: '[otel] trace_exporter' },
    },
  },
];

export const contentCategories: Switch<ContentCategory>[] = [
  {
    id: 'prompts',
    title: 'Промпты',
    notes: {
      claude: { text: 'В нативных событиях и трейсах' },
      codex: { text: 'В нативных событиях' },
    },
  },
  {
    id: 'assistant_responses',
    title: 'Ответы ассистента',
    notes: {
      claude: { text: 'В нативных событиях' },
      codex: { text: 'В нативных событиях' },
    },
  },
  {
    id: 'tool_details',
    title: 'Детали инструментов',
    notes: {
      claude: {
        tone: 'warn',
        text: 'В нативных событиях и трейсах. В метриках без них настоящие имена агентов, скиллов, плагинов и MCP-серверов заменяются обобщёнными — для метрик это не проверено',
      },
      codex: {
        tone: 'off',
        text: 'Не действует: у Codex нет такого ключа, аргументы инструментов уходят в нативных событиях всегда. Скрыть их можно только выключив нативные события',
      },
    },
  },
  {
    id: 'tool_content',
    title: 'Содержимое инструментов',
    notes: {
      claude: { text: 'В нативных трейсах' },
      codex: {
        tone: 'warn',
        text: 'Ограничено нативно: Codex только урезает вывод инструмента (tool_result.max_bytes), а не убирает его гарантированно; межагентные сообщения уходят всё равно. Полный вывод инструментов идёт через хуки',
      },
    },
  },
  {
    id: 'raw_api_bodies',
    title: 'Сырые тела API',
    notes: {
      claude: { text: 'В нативных событиях' },
      codex: { tone: 'off', text: 'Не относится: у Codex такого содержимого нет' },
    },
  },
];

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

export function setEvent(
  settings: TelemetrySettings,
  agent: AgentId,
  event: HookEvent,
  on: boolean,
): TelemetrySettings {
  const denied = <E extends HookEvent>(list: E[]) =>
    list.filter((id) => (id === event ? !on : !isEventOn(settings, agent, id)));
  const all = settings.agents ?? {};
  return agent === 'claude'
    ? {
        ...settings,
        agents: {
          ...all,
          claude: {
            enabled: true,
            ...all.claude,
            hook_events: { denied: denied(hookEvents.claude) },
          },
        },
      }
    : {
        ...settings,
        agents: {
          ...all,
          codex: { enabled: true, ...all.codex, hook_events: { denied: denied(hookEvents.codex) } },
        },
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
