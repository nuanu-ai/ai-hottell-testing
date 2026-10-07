import { useState } from 'react';

import type { components } from '../../shared/api';
import {
  Actions,
  Button,
  ConfirmDialog,
  CopyField,
  Hint,
  Notice,
  Panel,
  Stack,
  Tag,
} from '../../shared/ui';
import { useIssueMcpKey, useRevokeMcpKey } from './api/keys';
import { KeyDates } from './KeyDates';
import { claudeCodeCommand, codexConfigBlock } from './snippets';

type KeyStatus = components['schemas']['KeyStatus'];
type IssuedMcpKey = components['schemas']['IssuedMcpKey'];

// The open value is answered once, so the page holds it only until the user leaves.
function IssuedKey({ issued }: { issued: IssuedMcpKey }) {
  return (
    <Stack>
      <Notice tone="warn">
        Ключ показан один раз: больше он показан не будет. Скопируйте его или фрагмент для агента
        сейчас
      </Notice>
      <CopyField value={issued.key} />
      <h3>Claude Code</h3>
      <Hint>Выполните в терминале</Hint>
      <CopyField multiline value={claudeCodeCommand(issued)} />
      <h3>Codex</h3>
      <Hint>
        Добавьте в <code className="mono">~/.codex/config.toml</code>
      </Hint>
      <CopyField multiline value={codexConfigBlock(issued)} />
    </Stack>
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
    <Panel
      title="Ключ MCP"
      sub="С ним Claude Code и Codex подключаются к MCP-серверу сервиса. Ключ один на пользователя"
      action={
        status.active && (
          <Actions>
            <Button
              onClick={() => {
                setConfirming('reissue');
              }}
            >
              Перевыпустить
            </Button>
            <Button
              className="t-bad"
              onClick={() => {
                setConfirming('revoke');
              }}
            >
              Отозвать
            </Button>
          </Actions>
        )
      }
    >
      <Stack>
        {status.active ? (
          <Actions>
            <Tag tone="ok">активен</Tag>
            <KeyDates status={status} />
          </Actions>
        ) : (
          <>
            {issue.error && <Notice tone="err">{issue.error.message}</Notice>}
            <p>Ключа ещё нет</p>
            <Actions>
              <Button
                variant="primary"
                disabled={issue.isPending}
                onClick={() => {
                  issue.mutate(undefined, { onSuccess: setIssued });
                }}
              >
                Выпустить
              </Button>
            </Actions>
          </>
        )}
        {issued && <IssuedKey issued={issued} />}
      </Stack>
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
    </Panel>
  );
}
