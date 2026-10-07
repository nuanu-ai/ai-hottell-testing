import type { components } from '../../../shared/api';
import type { TeamPerson } from '../api/queries';

type User = components['schemas']['UserListItem'];

/** One person of «Команда»: the «Обзор» numbers over their own sessions; null is «—». */
export type TeamRow = {
  userId: string;
  name: string;
  isMe: boolean;
  /** «Ваши сессии» of «Обзор»: kind=user, no schedule and no service sessions. */
  sessions: number;
  userMin: number | null;
  agentMin: number | null;
  cost: number | null;
  /** Some sessions have no cost: the spend is a lower bound. */
  costPartial: boolean;
  errorRate: number | null;
  /** Sessions with at least one friction signal (the session's flags). */
  frictionSessions: number | null;
  /** The latest end (or start) among the person's sessions, ISO. */
  lastActive: string | null;
  /** Hook events by hour over the 5 days before today (HT-540); null when the server has none. */
  pulse: number[] | null;
};

/**
 * The rows of «Команда» (HT-435): every active person and everyone the server counted (HT-531), each
 * with the numbers «Обзор» shows over their sessions. A person without sessions gets 0 sessions and
 * «—» for the rest. Order: people with sessions first, the signed-in one at their top, then by name.
 */
export function teamRows(
  counted: readonly TeamPerson[],
  users: readonly User[],
  meId: string | undefined,
): TeamRow[] {
  const own = new Map(counted.map((person) => [person.user_id, person]));
  const names = new Map<string, string>();
  for (const person of counted) {
    names.set(person.user_id, person.user_name);
  }
  for (const u of users) {
    if (u.status === 'active') {
      names.set(u.id, u.name);
    }
  }

  const rows = [...names].map(([userId, name]): TeamRow => {
    const person = own.get(userId);
    const isMe = userId === meId;
    if (!person) {
      return {
        userId,
        name,
        isMe,
        sessions: 0,
        userMin: null,
        agentMin: null,
        cost: null,
        costPartial: false,
        errorRate: null,
        frictionSessions: null,
        lastActive: null,
        pulse: null,
      };
    }
    return {
      userId,
      name,
      isMe,
      sessions: person.sessions,
      userMin: person.user_min,
      agentMin: person.agent_min,
      cost: person.cost_usd,
      costPartial: person.cost_partial,
      errorRate: person.error_rate,
      frictionSessions: person.friction_sessions,
      lastActive: person.last_active,
      pulse: person.pulse?.values ?? null,
    };
  });

  return rows.sort(
    (a, b) =>
      Number(a.lastActive === null) - Number(b.lastActive === null) ||
      Number(b.isMe) - Number(a.isMe) ||
      a.name.localeCompare(b.name, 'ru'),
  );
}
