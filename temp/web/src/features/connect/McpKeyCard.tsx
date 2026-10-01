import { useState } from 'react';

import type { components } from '../../shared/api';
import { Badge, Button, Card, CopyField, Notice } from '../../shared/ui';
import { useIssueMcpKey, useRevokeMcpKey } from './api/keys';
import { ConfirmDialog } from './ConfirmDialog';
import { KeyDates } from './KeyDates';
import { claudeCodeCommand, codexConfigBlock } from './snippets';

type KeyStatus = components['schemas']['KeyStatus'];
type IssuedMcpKey = components['schemas']['IssuedMcpKey'];

// The open value is answered once, so the page holds it only until the user leaves.
function IssuedKey({ issued }: { issued: IssuedMcpKey }) {
  return (
    <div style={{ display: 'grid', gap: 14, marginTop: 16 }}>
      <Notice tone="warn">
        Ключ показан один раз: больше он показан не будет. Скопируйте его или фрагмент для агента
        сейчас
      </Notice>
      <CopyField value={issued.key} />
      <h3 style={{ margin: '8px 0 0' }}>Claude Code</h3>
      <p className="hint" style={{ margin: 0 }}>
        Выполните в терминале
      </p>
      <CopyField multiline value={claudeCodeCommand(issued)} />
      <h3 style={{ margin: '8px 0 0' }}>Codex</h3>
      <p className="hint" style={{ margin: 0 }}>
        Добавьте в <code className="mono">~/.codex/config.toml</code>
      </p>
      <CopyField multiline value={codexConfigBlock(issued)} />
    </div>
  );
}

type Confirming = 'reissue' | 'revoke' | null;

export function McpKeyCard({ status }: { status: KeyStatus }) {
  const issue = useIssueMcpKey();
  const reissue = useIssueMcpKey();
  const revoke = useRevokeMcpKey();
  const [issued, setIssued] = useState<IssuedMcpKey | null>(null);
  const [confirming, setConfirming] = useState<Confirming>(null);
  const closed = () => {
    setConfirming(null);
  };

  return (
    <Card>
      <h2>Ключ MCP</h2>
      <p className="hint">
        С ним Claude Code и Codex подключаются к MCP-серверу сервиса. Ключ один на пользователя
      </p>
      {status.active ? (
        <div style={{ display: 'grid', gap: 12 }}>
          <div className="row" style={{ gap: 12 }}>
            <Badge tone="ok">активен</Badge>
            <KeyDates status={status} />
          </div>
          <div className="row">
            <Button
              onClick={() => {
                setConfirming('reissue');
              }}
            >
              Перевыпустить
            </Button>
            <Button
              className="плохо"
              onClick={() => {
                setConfirming('revoke');
              }}
            >
              Отозвать
            </Button>
          </div>
        </div>
      ) : (
        <div style={{ display: 'grid', gap: 12 }}>
          {issue.error && <Notice tone="err">{issue.error.message}</Notice>}
          <p style={{ margin: 0 }}>Ключа ещё нет</p>
          <div className="row">
            <Button
              variant="primary"
              disabled={issue.isPending}
              onClick={() => {
                issue.mutate(undefined, { onSuccess: setIssued });
              }}
            >
              Выпустить
            </Button>
          </div>
        </div>
      )}
      {issued && <IssuedKey issued={issued} />}
      <ConfirmDialog
        open={confirming === 'reissue'}
        title="Перевыпустить ключ MCP"
        text="Перевыпустить ключ MCP? Прежний ключ перестанет работать: вставьте в Claude Code и Codex новые фрагменты."
        confirm="Перевыпустить"
        action={reissue}
        onDone={setIssued}
        onClosed={closed}
      />
      <ConfirmDialog
        open={confirming === 'revoke'}
        title="Отозвать ключ MCP"
        text="Отозвать ключ MCP? Агенты и бинарь больше не подключатся к MCP-серверу с ним."
        confirm="Отозвать"
        action={revoke}
        onDone={() => {
          setIssued(null);
        }}
        onClosed={closed}
      />
    </Card>
  );
}
