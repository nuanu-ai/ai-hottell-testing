import { Empty } from '../../../shared/ui';
import { SessionRetroActions } from './SessionRetroActions';

// No published report: start the retro in an agent (HT-317 buttons with the session-retro reply).
export function DeepEmpty({ sessionId, discuss = true }: { sessionId: string; discuss?: boolean }) {
  return (
    <Empty title="Глубокого разбора этой сессии нет">
      <p>Разбор делает ваш агент со skill session-retro и публикует его через MCP hottell.</p>
      {discuss && <SessionRetroActions sessionId={sessionId} />}
    </Empty>
  );
}
