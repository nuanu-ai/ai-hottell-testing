import type { components } from '../../../shared/api';

type SkillRow = components['schemas']['AnalyticsSkillRow'];

// skGroup of the reference: the group tag of a skill by its source path or plugin.
export function skillGroup(source: string): string {
  if (/^плагин/.test(source)) return 'плагины';
  if (/систем/.test(source)) return 'системные';
  if (/личные/.test(source)) return 'личные';
  if (/Claude/.test(source)) return 'Claude';
  if (/memories/.test(source)) return 'память Codex';
  return 'проекты';
}

// skName of the reference: a skill named just «skill» reads as «<its folder>/skill».
export function skillName(row: Pick<SkillRow, 'name' | 'source'>): string {
  if (row.name !== 'skill') return row.name;
  const folder = row.source.split('/').filter(Boolean).pop() ?? '';
  return `${folder}/skill`;
}

// The «остальные N skills» row of never-opened skills carries N in its source.
export function unusedCount(rows: readonly SkillRow[]): number | null {
  const row = rows.find((r) => r.state === 'unused');
  if (!row) return null;
  return Number.parseInt(/\d+/.exec(row.source)?.[0] ?? '0', 10);
}
