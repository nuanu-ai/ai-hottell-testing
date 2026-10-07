import { useQuery } from '@tanstack/react-query';

import { Grid, Loading, Notice, Panel, Stack } from '../../../shared/ui';
import { type DeepReportKey, deepReportQuery, pendingCandidate } from './api';
import { DeepCandidateNotice } from './DeepCandidateNotice';
import { DeepChecks } from './DeepChecks';
import { DeepEmpty } from './DeepEmpty';
import { DeepObservations } from './DeepObservations';
import { DeepTasks } from './DeepTasks';
import { checksSummary, deepChecks, deepObservations, deepTasks, deepUnknowns } from './model';

// The Deep part of the session screen: tasks, the 13 checks, observations and unknowns of the
// published report, and a notice when a newer candidate waits for its review; the «Разобрать»
// buttons when there is nothing. The screen owns the feed, so a line goes to onOpenLine.
export function DeepSessionReport({
  onOpenLine,
  discuss = true,
  ...key
}: DeepReportKey & {
  onOpenLine?: (line: number) => void;
  // false hides «Разобрать»: on another person's session the retro would run here (HT-444).
  discuss?: boolean;
}) {
  const report = useQuery(deepReportQuery(key));
  if (report.isPending) return <Loading />;
  if (report.isError) {
    return <Notice tone="err">Не удалось загрузить глубокий разбор: {report.error.message}</Notice>;
  }
  const candidate = report.data ? pendingCandidate(report.data) : undefined;
  const notice = candidate && (
    <DeepCandidateNotice sessionId={key.sessionId} candidate={candidate} discuss={discuss} />
  );
  const published = report.data?.published;
  if (!published) return notice ?? <DeepEmpty sessionId={key.sessionId} discuss={discuss} />;
  const tasks = deepTasks(published.deep);
  const checks = deepChecks(published.deep);
  return (
    <Stack gap={12}>
      <Grid>
        <Panel span={6} title="Задания и исход" sub={String(tasks.length)}>
          <DeepTasks tasks={tasks} onOpenLine={onOpenLine} />
        </Panel>
        <Panel span={6} title="13 проверок" sub={checksSummary(checks)}>
          <DeepChecks checks={checks} onOpenLine={onOpenLine} />
        </Panel>
        <Panel span={12} title="Выводы разбора">
          <DeepObservations
            observations={deepObservations(published.deep)}
            unknowns={deepUnknowns(published.deep)}
            onOpenLine={onOpenLine}
          />
        </Panel>
      </Grid>
      {notice}
    </Stack>
  );
}
