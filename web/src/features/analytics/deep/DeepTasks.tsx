import { Empty, Tag } from '../../../shared/ui';
import { LineLink } from './LineLink';
import { type DeepTask, OUTCOME_LABELS, labelOf } from './model';

// The report's tasks: id, goal with its line range, outcome (sess-tasks in the v5.1 page).
export function DeepTasks({
  tasks,
  onOpenLine,
}: {
  tasks: readonly DeepTask[];
  onOpenLine?: (line: number) => void;
}) {
  if (tasks.length === 0) return <Empty title="Заданий нет" />;
  return (
    <div className="deep-tasks">
      {tasks.map((t, index) => {
        const [label, tone] = labelOf(OUTCOME_LABELS, t.outcome);
        return (
          <div className="task" key={`${t.id}:${String(index)}`}>
            <span className="mono muted">{t.id}</span>
            <span>
              {t.goal || '—'}
              {t.startLine !== null && (
                <>
                  <br />
                  <LineLink
                    line={t.startLine}
                    label={
                      t.endLine !== null && t.endLine !== t.startLine
                        ? `L${String(t.startLine)}–${String(t.endLine)}`
                        : undefined
                    }
                    onOpen={onOpenLine}
                  />
                </>
              )}
            </span>
            <Tag tone={tone}>{label}</Tag>
          </div>
        );
      })}
    </div>
  );
}
