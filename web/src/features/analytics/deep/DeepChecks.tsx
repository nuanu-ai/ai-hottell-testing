import { Tag } from '../../../shared/ui';
import { LineLinks } from './LineLink';
import { CHECK_LABELS, type DeepCheck, labelOf } from './model';

// «Нет данных» names what the check lacked when the report says so.
function hintOf(check: DeepCheck): string | undefined {
  if (check.missingData.length > 0) return `Не хватает данных: ${check.missingData.join('; ')}`;
  if (check.status === null) return 'В разборе нет результата этой проверки';
  return undefined;
}

// The 13 checks with their status, summary and lines (sess-checks in the v5.1 page).
export function DeepChecks({
  checks,
  onOpenLine,
}: {
  checks: readonly DeepCheck[];
  onOpenLine?: (line: number) => void;
}) {
  return (
    <div className="deep-checks">
      {checks.map((c) => {
        const [label, tone] = labelOf(CHECK_LABELS, c.status);
        return (
          <div className="ck" key={c.id} data-check={c.id}>
            <span className="mono">{c.id}</span>
            <span>{c.name}</span>
            <span>
              <Tag tone={tone} title={hintOf(c)}>
                {label}
              </Tag>
            </span>
            <span className="s">
              {c.summary || (c.status === null ? 'В разборе нет результата этой проверки.' : '')}{' '}
              <LineLinks lines={c.lines} onOpen={onOpenLine} />
            </span>
          </div>
        );
      })}
    </div>
  );
}
