import { useQuery } from '@tanstack/react-query';

import { Button, Card, Notice, PageTitle } from '../../shared/ui';
import { keysQuery } from './api/keys';
import { CollectorTokenCard } from './CollectorTokenCard';
import { McpKeyCard } from './McpKeyCard';

export function ConnectPage() {
  return (
    <>
      <div style={{ marginBottom: 16 }}>
        <PageTitle>Подключение</PageTitle>
      </div>
      <Keys />
    </>
  );
}

function Keys() {
  const keys = useQuery(keysQuery);

  if (keys.isPending) {
    return (
      <Card>
        <div className="loading">
          <span className="spinner" />
          Загружаем ключи…
        </div>
      </Card>
    );
  }

  if (keys.isError) {
    return (
      <Card>
        <Notice tone="err">
          {keys.error.message}{' '}
          <Button
            size="sm"
            disabled={keys.isFetching}
            onClick={() => {
              void keys.refetch();
            }}
          >
            Повторить
          </Button>
        </Notice>
      </Card>
    );
  }

  return (
    <>
      <McpKeyCard status={keys.data.mcp} />
      <CollectorTokenCard status={keys.data.ingest} />
    </>
  );
}
