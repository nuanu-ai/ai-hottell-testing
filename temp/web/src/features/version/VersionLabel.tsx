import { useQuery } from '@tanstack/react-query';

import { Hint } from '../../shared/ui';
import { versionQuery } from './api/queries';

export function VersionLabel() {
  const { data, isPending, isError } = useQuery(versionQuery);

  if (isPending) {
    return <Hint>Версия: загрузка…</Hint>;
  }
  if (isError) {
    return <Hint>Версия: недоступна</Hint>;
  }
  return <Hint>Версия: {data.version}</Hint>;
}
