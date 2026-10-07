import { useRef, useState } from 'react';

import { Button } from './Button';
import { CopyField } from './CopyField';

export type DiscussAgent = 'codex' | 'claude';

// The reply and the command come from the caller (the coach in E4): the component knows no texts.
export type DiscussAction = {
  agent: DiscussAgent;
  label: string;
  primary?: boolean;
  reply: string;
  command: string;
};

const AGENT_NAMES: Record<DiscussAgent, string> = { codex: 'Codex', claude: 'Claude Code' };
const COPIED_FOR_MS = 1600;

// The row remembers the action it was opened for: reply and command as copied.
type Open = { index: number; copied: boolean; reply: string; command: string };

// «Обсудить в Codex / в Claude Code»: a click copies the reply and opens the command row,
// as discussBtns/discussRow in the v5.1 page.
export function DiscussActions({
  actions,
  size,
}: {
  actions: readonly DiscussAction[];
  size?: 'sm' | 'lg';
}) {
  const [open, setOpen] = useState<Open | null>(null);
  // Only the latest click may open the row: an earlier copy that settles late is dropped.
  const clickRef = useRef(0);

  async function discuss(index: number, reply: string, command: string) {
    const click = ++clickRef.current;
    let copied = true;
    try {
      await navigator.clipboard.writeText(reply);
    } catch {
      copied = false;
    }
    if (click === clickRef.current) setOpen({ index, copied, reply, command });
  }

  const action = open ? actions[open.index] : undefined;
  // The action changed under the open row (another period, another topic): what was copied is no
  // longer what the row would show, so the row closes; a copy settling late closes here too.
  if (open && (action?.reply !== open.reply || action.command !== open.command)) {
    setOpen(null);
  }
  return (
    <div className="discuss">
      <div className="acts">
        {actions.map((a, index) => (
          <Button
            key={a.agent}
            variant={a.primary ? 'primary' : undefined}
            size={size}
            onClick={() => void discuss(index, a.reply, a.command)}
          >
            {a.label}
          </Button>
        ))}
      </div>
      {open && action && (
        <div className="cmd">
          <span className="m">
            {open.copied ? (
              `Реплика скопирована — вставьте её в ${AGENT_NAMES[action.agent]} или запустите в терминале:`
            ) : (
              <>
                Скопируйте реплику вручную: <code>{action.reply}</code> или запустите в терминале:
              </>
            )}
          </span>
          <div className="c">
            <CopyField
              key={open.index}
              value={action.command}
              title={action.command}
              copiedForMs={COPIED_FOR_MS}
            />
            <Button
              variant="link"
              className="x"
              onClick={() => {
                clickRef.current++;
                setOpen(null);
              }}
            >
              Закрыть
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
