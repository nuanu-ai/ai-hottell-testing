// The chosen sessions of a page (README v5.1 «База подсчётов»): their ids, and whether they are every
// session of the dataset — then a row's own totals stand and by_session is not needed.
export type Selection = { ids: ReadonlySet<string>; full: boolean };

type BySession<K extends string> =
  | Readonly<Record<string, number | Readonly<Partial<Record<K, number | null>>> | null>>
  | null
  | undefined;

// The note for durations taken over the whole window: only counts are split by session.
export function windowTimeNote(selection: Selection): string {
  return selection.full ? '' : ' · время за всё окно, не по выбранным сессиям';
}

// bySess of the reference: a field of a row over the chosen sessions. The row's total when every
// session is chosen; otherwise the sum over by_session, and null when a chosen session has no count.
export function countIn<K extends string>(
  total: number | null | undefined,
  bySession: BySession<K>,
  selection: Selection,
  key: K,
): number | null {
  if (selection.full) return total ?? null;
  if (!bySession) return null;
  let sum = 0;
  for (const [sid, value] of Object.entries(bySession)) {
    if (!selection.ids.has(sid)) continue;
    const count = typeof value === 'number' ? value : value?.[key];
    if (count == null) return null;
    sum += count;
  }
  return sum;
}
