// One bar of the pulse feed (CONTRACT.md «/api/pulse»): n hook events of one agent in the 10 s from t.
export type PulseBar = { t: string; agent: 'claude' | 'codex'; n: number };
export type PulseSlot = { claude: number; codex: number };

const SLOT_MS = 10_000;
export const SLOTS = 60;

/** 60 slots of 10 s each, the last one the slot `now` falls into (pulseSlots in the reference). */
export function pulseSlots(
  bars: readonly PulseBar[],
  now: number,
): { now: number; slots: PulseSlot[] } {
  const current = Math.floor(now / SLOT_MS) * SLOT_MS;
  const slots = Array.from({ length: SLOTS }, () => ({ claude: 0, codex: 0 }));
  for (const bar of bars) {
    const i = SLOTS - 1 - Math.round((current - Date.parse(bar.t)) / SLOT_MS);
    const slot = slots[i];
    if (slot) slot[bar.agent] += bar.n;
  }
  return { now: current, slots };
}
