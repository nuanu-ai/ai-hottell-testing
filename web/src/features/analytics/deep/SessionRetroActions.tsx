import { discussActions } from '../../../shared/lib/discuss';
import { DiscussActions } from '../../../shared/ui';

// «Разобрать в Codex / в Claude Code»: copies $session-retro сессия <id> (decision 11 of HT-159);
// the session head's discussSlot and the empty Deep place both use it.
export function SessionRetroActions({
  sessionId,
  size,
}: {
  sessionId: string;
  size?: 'sm' | 'lg';
}) {
  return (
    <DiscussActions
      size={size}
      actions={discussActions({ mode: 'retro', sessionId }, [
        'Разобрать в Codex',
        'Разобрать в Claude Code',
      ])}
    />
  );
}
