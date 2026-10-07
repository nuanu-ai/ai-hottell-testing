import type { DiscussAction, DiscussAgent } from '../ui';
import { plural } from './format';

// What a «Обсудить» button asks the agent about: the coach on a subject («обзор» or a topic id)
// over the chosen days (null: the whole period), a retro of one session, the independent
// review of a session's retro that waits as a candidate, or a fresh skills analysis.
export type DiscussSubject =
  | { mode: 'coach'; subject: string; days: number | null }
  | { mode: 'retro'; sessionId: string }
  | { mode: 'review'; sessionId: string }
  | { mode: 'skills' };

const SKILL_PREFIX: Record<DiscussAgent, string> = { codex: '$', claude: '/' };
const AGENT_COMMAND: Record<DiscussAgent, string> = { codex: 'codex', claude: 'claude' };

// The reply pasted into the agent, as coachReply in the v5.1 page.
export function discussReply(agent: DiscussAgent, about: DiscussSubject): string {
  const prefix = SKILL_PREFIX[agent];
  if (about.mode === 'retro') return `${prefix}session-retro сессия ${about.sessionId}`;
  if (about.mode === 'review') return `${prefix}session-retro review ${about.sessionId}`;
  if (about.mode === 'skills') return `${prefix}session-retro skills`;
  const period =
    about.days === null
      ? 'весь период'
      : `период ${String(about.days)} ${plural(about.days, 'день', 'дня', 'дней')}`;
  return `${prefix}hottell-coach ${about.subject}, ${period}`;
}

// The terminal command that starts the agent with the reply, single-quoted for the shell.
export function discussCommand(agent: DiscussAgent, about: DiscussSubject): string {
  return `${AGENT_COMMAND[agent]} ${shellQuote(discussReply(agent, about))}`;
}

// The two actions DiscussActions shows: Codex first and primary, then Claude Code.
export function discussActions(
  about: DiscussSubject,
  labels: readonly [string, string] = ['Обсудить в Codex', 'Обсудить в Claude Code'],
): DiscussAction[] {
  return [
    {
      agent: 'codex',
      label: labels[0],
      primary: true,
      reply: discussReply('codex', about),
      command: discussCommand('codex', about),
    },
    {
      agent: 'claude',
      label: labels[1],
      reply: discussReply('claude', about),
      command: discussCommand('claude', about),
    },
  ];
}

function shellQuote(s: string): string {
  return `'${s.replaceAll("'", "'\\''")}'`;
}
