import { useState } from 'react';

import type { components } from '../../shared/api';
import { Badge, Button, Card } from '../../shared/ui';
import { useReissueIngestToken } from './api/keys';
import { ConfirmDialog } from './ConfirmDialog';
import { KeyDates } from './KeyDates';

type KeyStatus = components['schemas']['KeyStatus'];

// The token itself is never shown: only the binary gets it, through MCP.
export function CollectorTokenCard({ status }: { status: KeyStatus }) {
  const reissue = useReissueIngestToken();
  const [confirming, setConfirming] = useState(false);

  return (
    <Card>
      <h2>Токен коллектора</h2>
      <p className="hint">
        С ним бинарь hottell отправляет телеметрию. Токен один на пользователя, общий для всех
        машин; бинарь получает его через MCP
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
                setConfirming(true);
              }}
            >
              Перевыпустить
            </Button>
          </div>
        </div>
      ) : (
        <p style={{ margin: 0 }}>
          Токена ещё нет: бинарь получит его через MCP при первом подключении
        </p>
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
    </Card>
  );
}
