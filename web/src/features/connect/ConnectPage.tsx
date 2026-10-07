import { useQuery } from '@tanstack/react-query';

import { LoadError, Loading, Notice, PageHead, View } from '../../shared/ui';
import { keysQuery } from './api/keys';
import { CollectorTokenCard } from './CollectorTokenCard';
import { McpKeyCard } from './McpKeyCard';

export function ConnectPage() {
  return (
    <View>
      <PageHead title="Подключение" />
      {/* Everyone signed in sees everyone's data (HT-188): say it before the config fragments. */}
      <Notice tone="warn">
        Ваши сессии, включая реплики, видят все участники; ленты, пути и ветки — тоже.
      </Notice>
      <Keys />
    </View>
  );
}

function Keys() {
  const keys = useQuery(keysQuery);

  if (keys.isPending) {
    return <Loading>Загружаем ключи…</Loading>;
  }

  if (keys.isError) {
    return (
      <LoadError
        error={keys.error}
        retrying={keys.isFetching}
        onRetry={() => {
          void keys.refetch();
        }}
      />
    );
  }

  return (
    <>
      <McpKeyCard status={keys.data.mcp} />
      <CollectorTokenCard status={keys.data.ingest} />
    </>
  );
}
