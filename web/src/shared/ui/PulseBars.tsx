import { useEffect, useRef } from 'react';

import { fmtN, plural } from '../lib/format';
import { classNames } from './classNames';
import { play } from './motion';
import { pulseSlots, SLOTS } from './pulseSlots';
import type { PulseBar, PulseSlot } from './pulseSlots';

const empty = (slot: PulseSlot) => slot.claude + slot.codex === 0;
const barHeight = (n: number) => Math.min(16, Math.round((n / 12) * 16));

function pulseLabel(
  slots: PulseSlot[],
  active: number,
  error: string | undefined,
  silent: boolean,
) {
  if (error) return 'пульс недоступен';
  if (silent) {
    let trail = 0;
    for (let i = slots.length - 1; i >= 0 && empty(slots[i] ?? { claude: 0, codex: 0 }); i--)
      trail++;
    return trail >= SLOTS
      ? 'тишина 10+ мин'
      : `тишина ${String(Math.max(2, Math.round((trail * 10) / 60)))} мин`;
  }
  const perMin = slots.slice(-6).reduce((sum, s) => sum + s.claude + s.codex, 0);
  return (
    `${String(active)} ${plural(active, 'работает', 'работают', 'работают')} · ` +
    `${fmtN(perMin)} ${plural(perMin, 'событие', 'события', 'событий')}/мин`
  );
}

type PulseBarsProps = {
  bars: readonly PulseBar[];
  /** The current moment in ms; the caller's clock, so the bars and the poll agree. */
  now: number;
  /** How many sessions are working now. */
  active: number;
  /** Why the pulse could not be read; the bars go grey. */
  error?: string;
  /** Whether the «Идут сейчас» list under the button is open. */
  expanded?: boolean;
  onClick?: () => void;
};

// The header pulse (README v5.1 «Анимация: Пульс»): 60 bars over 10 minutes, Claude stacked on Codex,
// with a dot that rings on new events and bars that shift left on each new slot.
export function PulseBars({ bars, now, active, error, expanded, onClick }: PulseBarsProps) {
  const { now: slot, slots } = pulseSlots(bars, now);
  const silent = Boolean(error) || slots.slice(-12).every(empty);
  const latest = bars.reduce((max, bar) => Math.max(max, Date.parse(bar.t)), 0);

  const barsRef = useRef<HTMLSpanElement>(null);
  const ringRef = useRef<HTMLElement>(null);
  const seen = useRef<{ slot: number; latest: number } | null>(null);

  useEffect(() => {
    const before = seen.current;
    seen.current = { slot, latest };
    if (!before) return;
    if (slot > before.slot) {
      play(
        barsRef.current,
        [{ transform: 'translateX(3px)' }, { transform: 'translateX(0)' }],
        400,
      );
    }
    if (latest > before.latest) {
      play(
        ringRef.current,
        [
          { transform: 'scale(1)', opacity: 1 },
          { transform: 'scale(1.6)', opacity: 0 },
        ],
        600,
      );
    }
  }, [slot, latest]);

  return (
    <button
      type="button"
      className="pbtn"
      title={error ?? 'События хуков за 10 минут, столбик — 10 секунд'}
      aria-expanded={expanded}
      onClick={onClick}
    >
      <span className="pbox">
        <span className="pbars" ref={barsRef}>
          {slots.map((s, i) => (
            <span key={i}>
              <i
                style={{
                  height: `${String(empty(s) ? 1 : barHeight(s.codex))}px`,
                  background: empty(s) || silent ? 'var(--line)' : 'var(--ok)',
                }}
              />
              <i
                style={{
                  height: `${String(barHeight(s.claude))}px`,
                  background: silent ? 'var(--line)' : 'var(--accent)',
                }}
              />
            </span>
          ))}
        </span>
      </span>
      <span className={classNames('pdot', silent && 'off')}>
        <i className="ring" ref={ringRef} />
        <i />
      </span>
      <span className="plabel">{pulseLabel(slots, active, error, silent)}</span>
    </button>
  );
}
