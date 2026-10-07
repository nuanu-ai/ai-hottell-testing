import type { components } from '../../../shared/api';

type Session = components['schemas']['AnalyticsSession'];

// Where a session's dollars come from, for the title next to the figure.
export function costTitle(session: Pick<Session, 'cost_usd' | 'cost_basis' | 'agent'>): string {
  if (session.cost_usd == null) return 'стоимость не записана';
  if (session.cost_basis === 'otel_reported') return 'по данным OTel Claude Code';
  // A Claude session without native OTel: its tokens from the transcript at the model's price.
  return session.agent === 'claude' ? 'оценка по цене API модели' : 'оценка по условной цене API';
}

// The kind of a session in the live data; «Исход» stays for the Deep review (E4).
export const KINDS: Record<Session['kind'], { text: string; cls: string }> = {
  user: { text: 'ваша', cls: 'tag ok' },
  system: { text: 'служебная', cls: 'tag muted' },
  automation: { text: 'расписание', cls: 'tag acc' },
  agent: { text: 'агентская', cls: 'tag muted' },
};
