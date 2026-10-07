import type { components } from '../../../shared/api';
import type { TagTone } from '../../../shared/ui';

type Session = components['schemas']['AnalyticsSession'];

export type SourceRow = { label: string; tag: string; tone: TagTone; note: string };

const STATUS: Record<string, [string, TagTone]> = {
  recorded: ['записано', 'ok'],
  linked: ['привязан', 'ok'],
  partial: ['частично', 'warn'],
  unlinked: ['не привязан', 'warn'],
  missing: ['нет', 'plain'],
};

function status(value: string | null | undefined): [string, TagTone] {
  return (value && STATUS[value]) || [value || '—', 'plain'];
}

/** «Откуда данные» of the live data (reference renderSources). */
export function sourceRows(s: Session): SourceRow[] {
  const { hooks, otel } = s.sources;
  const facts = s.sources.transcript_facts === 'recorded';
  const otelNote =
    otel === 'unlinked'
      ? facts
        ? 'OTel пишется без ID сессии; расход и ответы модели взяты из журнала сессии через hottell'
        : 'события без ID сессии: расход по сессии не виден'
      : otel === 'missing'
        ? 'OTel этой сессии не собирался'
        : 'привязан к этой сессии';
  const row = (label: string, value: string, note: string): SourceRow => {
    const [tag, tone] = status(value);
    return { label, tag, tone, note };
  };
  return [
    row(
      'Hooks hottell',
      hooks,
      hooks === 'partial'
        ? 'запись началась не с начала сессии — начало не записано'
        : 'события хуков hottell из ClickHouse стенда',
    ),
    row('Нативный OTel', otel, otelNote),
    row(
      'Факты журнала',
      facts ? 'recorded' : 'missing',
      facts ? 'код выхода и токены за ход дочитаны hottell' : 'hottell не дочитывал журнал сессии',
    ),
  ];
}

const PER_SESSION = /^[0-9a-f]{8}(…[0-9a-f]{4})?:\s*/;

/**
 * The dataset's gaps that name this session (prefix «id8:» or «id8…id4:»), the prefix cut off.
 * Empty when the session has none.
 */
export function sessionGaps(gaps: readonly string[], id: string): string[] {
  const prefixes = [`${id.slice(0, 8)}:`, `${id.slice(0, 8)}…${id.slice(-4)}:`];
  return gaps
    .filter((g) => prefixes.some((p) => g.startsWith(p)))
    .map((g) => g.replace(PER_SESSION, ''));
}

/** A tool's name for the list: `mcp__srv__tool` as «srv · tool». */
export function toolLabel(name: string): string {
  return name
    .replace(/^mcp__/, '')
    .split('__')
    .join(' · ');
}
