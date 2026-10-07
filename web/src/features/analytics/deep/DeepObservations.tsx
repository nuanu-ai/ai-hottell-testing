import { Empty, Tag } from '../../../shared/ui';
import { LineLinks } from './LineLink';
import { type DeepObservation, OBSERVATION_LABELS, labelOf } from './model';

// Findings outside the 13 checks and what the retro could not establish (sess-deep in the v5.1 page).
export function DeepObservations({
  observations,
  unknowns,
  onOpenLine,
}: {
  observations: readonly DeepObservation[];
  unknowns: readonly string[];
  onOpenLine?: (line: number) => void;
}) {
  if (observations.length === 0 && unknowns.length === 0) return <Empty title="Выводов нет" />;
  return (
    <div className="deep-obs">
      {observations.map((o, index) => {
        const [label, tone] = labelOf(OBSERVATION_LABELS, o.status);
        return (
          <div className="obs" key={`${o.pattern}:${String(index)}`}>
            <span>
              <Tag tone={tone}>{label}</Tag> <b>{o.pattern}</b>
              {o.taskIds.length > 0 && (
                <span className="muted mono"> · {o.taskIds.join(', ')}</span>
              )}
            </span>
            <span>{o.finding}</span>
            <LineLinks lines={o.lines} limit={8} onOpen={onOpenLine} />
          </div>
        );
      })}
      {unknowns.length > 0 && (
        <div className="obs deep-unknowns">
          <span className="lbl">Чего не знаем</span>
          <ul>
            {unknowns.map((u, index) => (
              <li key={index}>{u}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
