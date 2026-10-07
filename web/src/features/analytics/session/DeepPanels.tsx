import { type DeepReportKey } from '../deep/api';
import { DeepSessionReport } from '../deep/DeepSessionReport';

// E4: Deep-разбор сессии — задания и исход, 13 проверок, выводы, or the «Разобрать» buttons
// when the session has no published report. A line opens this session's feed on it. The owner
// and agent narrow the report to this session: another user's session may share its id.
export function DeepPanels({
  sessionId,
  user,
  agent,
  onOpenLine,
  discuss,
}: DeepReportKey & {
  onOpenLine?: (line: number) => void;
  // false on another person's session: the retro runs on the viewer's machine (HT-444).
  discuss?: boolean;
}) {
  return (
    <DeepSessionReport
      sessionId={sessionId}
      user={user}
      agent={agent}
      onOpenLine={onOpenLine}
      discuss={discuss}
    />
  );
}
