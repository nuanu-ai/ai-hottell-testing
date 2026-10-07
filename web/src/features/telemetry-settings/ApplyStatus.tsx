import { useQuery } from '@tanstack/react-query';

import { Tag } from '../../shared/ui';
import { appliedVersionQuery } from './api/settings';

// Whether the binary has taken the saved settings yet, by the version of its last report.
// A failed read hides the line: it is a hint, not a part of the page that can break it.
export function ApplyStatus({ saved }: { saved: number }) {
  const delivery = useQuery(appliedVersionQuery(saved));

  // A failed poll keeps the data of the last read: hide that, it is stale.
  if (!delivery.data || delivery.isError) {
    return null;
  }
  if (delivery.data.state === 'not_configured') {
    return <Tag tone="plain">бинарь не подключён</Tag>;
  }
  // settings_version is the server's own copy; the binary's is applied_settings_version.
  const applied = delivery.data.applied_settings_version;
  if (applied !== undefined && applied >= saved) {
    return <Tag tone="ok">Бинарь применил v{applied}</Tag>;
  }
  return (
    <Tag tone="warn">
      ждём бинарь ({applied === undefined ? 'версия неизвестна' : `v${String(applied)}`} &lt; v
      {saved})
    </Tag>
  );
}
