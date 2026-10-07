import { useState } from 'react';

import type { components } from '../../shared/api';
import { Actions, Button, ConfirmDialog, Panel, Tag } from '../../shared/ui';
import { useReissueIngestToken } from './api/keys';
import { KeyDates } from './KeyDates';

type KeyStatus = components['schemas']['KeyStatus'];

// The token itself is never shown: only the binary gets it, through MCP.
export function CollectorTokenCard({ status }: { status: KeyStatus }) {
  const reissue = useReissueIngestToken();
  const [confirming, setConfirming] = useState(false);

  return (
    <Panel
      title="Токен коллектора"
      sub="С ним бинарь hottell отправляет телеметрию. Токен один на пользователя, общий для всех машин; бинарь получает его через MCP"
      action={
        status.active && (
          <Button
            onClick={() => {
              setConfirming(true);
            }}
          >
            Перевыпустить
          </Button>
        )
      }
    >
      {status.active ? (
        <Actions>
          <Tag tone="ok">активен</Tag>
          <KeyDates status={status} />
        </Actions>
      ) : (
        <p>Токена ещё нет: бинарь получит его через MCP при первом подключении</p>
      )}
      <ConfirmDialog
        open={confirming}
        title="Перевыпустить токен коллектора"
        text="Перевыпустить токен коллектора? Отправка телеметрии со всех машин остановится, пока бинарь не получит новый токен через MCP."
        confirm="Перевыпустить"
        action={reissue}
        onClosed={() => {
          setConfirming(false);
        }}
      />
    </Panel>
  );
}
