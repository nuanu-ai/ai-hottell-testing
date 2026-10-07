import { Legend } from './Bits';
import { classNames } from './classNames';

// An error share is drawn at least this wide, so a single error stays visible.
const MIN_ERROR_PCT = 1.5;

const pct = (value: number) => `${String(Number(value.toFixed(1)))}%`;

type OutcomeBarProps = {
  calls: number;
  errors: number;
  // null: result and «нет результата» are not split for the chosen sessions.
  unknown: number | null;
};

// The call outcome of a tool, as .obar in renderTools of the v5.1 page: green result,
// red error, the rest «нет результата» in --line.
export function OutcomeBar({ calls, errors, unknown }: OutcomeBarProps) {
  const result = unknown === null ? null : Math.max(0, calls - errors - unknown);
  const ok = result === null || !calls ? 0 : (result / calls) * 100;
  const error = errors && calls ? Math.max((errors / calls) * 100, MIN_ERROR_PCT) : 0;
  const label =
    result === null
      ? `ошибок ${String(errors)}, результат и нет результата не разделены`
      : `результат ${String(result)}, ошибок ${String(errors)}, нет результата ${String(unknown)}`;
  return (
    <div className={classNames('obar', result === null && 'nosplit')} role="img" aria-label={label}>
      {ok > 0 && <i style={{ width: pct(ok), background: 'var(--ok)' }} />}
      {error > 0 && <i style={{ width: pct(error), background: 'var(--bad)' }} />}
    </div>
  );
}

// The hatch of a bar whose result and «нет результата» are not split.
const NOT_SPLIT = 'repeating-linear-gradient(135deg, var(--line) 0 3px, var(--surface-2) 3px 6px)';

export function OutcomeLegend({ notSplit }: { notSplit?: boolean }) {
  return (
    <Legend
      items={[
        { label: 'результат', color: 'var(--ok)' },
        { label: 'ошибка', color: 'var(--bad)' },
        { label: 'нет результата', color: 'var(--line)' },
        ...(notSplit ? [{ label: 'не разделено по сессиям', color: NOT_SPLIT }] : []),
      ]}
    />
  );
}
