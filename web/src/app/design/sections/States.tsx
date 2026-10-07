import { useRef, useState } from 'react';

import {
  Actions,
  Button,
  ConfirmDialog,
  CopyField,
  Dialog,
  Empty,
  Hint,
  LoadError,
  Loading,
  Notice,
  Panel,
} from '../../../shared/ui';

// The showcase has no server: the action fails, so the dialog shows its error and stays open.
const DEMO_ACTION = {
  error: new Error('Демо: сервер не отвечает, окно остаётся открытым.'),
  isPending: false,
  mutate: () => undefined,
  reset: () => undefined,
};

export function StatesSection() {
  const dialog = useRef<HTMLDialogElement>(null);
  const [confirming, setConfirming] = useState(false);

  return (
    <Panel
      title="Сообщения и состояния"
      sub="Notice · LoadError · Empty · Loading · Hint · CopyField · Dialog · ConfirmDialog"
    >
      <Notice tone="err">Не удалось сохранить: сервер недоступен.</Notice>
      <Notice tone="ok">Настройки сохранены.</Notice>
      <Notice tone="info">Ключ показывается один раз.</Notice>
      <Notice tone="warn">Сессия идёт: последние вызовы ещё не записаны.</Notice>
      <LoadError
        error={new Error('Не удалось загрузить ключи: сервер недоступен.')}
        retrying={false}
        onRetry={() => undefined}
      />
      <Empty title="Пока пусто">
        <p>Пригласите первого пользователя.</p>
      </Empty>
      {/* Inside a panel Loading has no frame of its own. */}
      <Loading />
      <Hint>Подсказка под полем или блоком.</Hint>
      <CopyField value="claude mcp add --transport http hottell https://hottell.example/mcp" />
      <CopyField multiline value={'[mcp_servers.hottell]\nurl = "https://hottell.example/mcp"'} />
      <Actions>
        <Button
          onClick={() => {
            dialog.current?.showModal();
          }}
        >
          Открыть диалог
        </Button>
        <Button
          className="t-bad"
          onClick={() => {
            setConfirming(true);
          }}
        >
          Подтверждение
        </Button>
      </Actions>
      <Dialog ref={dialog} title="Перевыпустить ключ?">
        <p>Старый ключ перестанет работать сразу.</p>
        <Actions justify="end" className="dialog-acts">
          <Button
            onClick={() => {
              dialog.current?.close();
            }}
          >
            Отмена
          </Button>
          <Button variant="primary">Перевыпустить</Button>
        </Actions>
      </Dialog>
      <ConfirmDialog
        open={confirming}
        title="Отозвать ключ MCP"
        text="Агенты и бинарь больше не подключатся к MCP-серверу с ним."
        confirm="Отозвать"
        action={DEMO_ACTION}
        onClosed={() => {
          setConfirming(false);
        }}
      />
    </Panel>
  );
}
