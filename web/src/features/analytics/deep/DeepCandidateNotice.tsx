import { discussActions } from '../../../shared/lib/discuss';
import { formatDateTime } from '../../../shared/lib/time';
import { DiscussActions, Notice } from '../../../shared/ui';
import type { DeepCandidate } from './api';

// A candidate is not in the registry until an independent review approves it; the person sees
// that the retro waits and how to start the review. A rejected candidate needs a corrected retro.
export function DeepCandidateNotice({
  sessionId,
  candidate,
  discuss = true,
}: {
  sessionId: string;
  candidate: DeepCandidate;
  // false hides «Разобрать»: on another person's session the retro would run here (HT-444).
  discuss?: boolean;
}) {
  const since = formatDateTime(new Date(candidate.submitted_at));
  if (candidate.status === 'rejected') {
    return (
      <Notice tone="warn">
        Проверка не приняла разбор от {since}: исправьте его и отправьте снова.
        {discuss && (
          <DiscussActions
            size="sm"
            actions={discussActions({ mode: 'retro', sessionId }, [
              'Разобрать в Codex',
              'Разобрать в Claude Code',
            ])}
          />
        )}
      </Notice>
    );
  }
  return (
    <Notice tone="info">
      Разбор на проверке с {since} — запустите <code>$session-retro review {sessionId}</code>
      <DiscussActions
        size="sm"
        actions={discussActions({ mode: 'review', sessionId }, [
          'Проверить в Codex',
          'Проверить в Claude Code',
        ])}
      />
    </Notice>
  );
}
