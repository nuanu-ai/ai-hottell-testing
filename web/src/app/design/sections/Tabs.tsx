import { useState } from 'react';

import { Panel, TabPanel, Tabs } from '../../../shared/ui';

const TABS = [
  { id: 'general', label: 'Общие' },
  { id: 'hooks', label: 'Хуки', badge: 3 },
  { id: 'privacy', label: 'Приватность', badge: 'dot' },
] as const;

type TabId = (typeof TABS)[number]['id'];

const TEXT: Record<TabId, string> = {
  general: 'Общие настройки: имя машины, период хранения.',
  hooks: 'Хуки: три найдены у агентов, бейдж показывает их число.',
  privacy: 'Приватность: точка на вкладке значит «не сохранено».',
};

export function TabsSection() {
  const [tab, setTab] = useState<TabId>('general');

  return (
    <Panel title="Вкладки" sub="Tabs · TabPanel · мышь, ←/→, Home/End">
      <Tabs id="design-tabs" label="Разделы витрины" tabs={TABS} value={tab} onChange={setTab} />
      <TabPanel tabsId="design-tabs" tab={tab}>
        <p className="muted">{TEXT[tab]}</p>
      </TabPanel>
    </Panel>
  );
}
