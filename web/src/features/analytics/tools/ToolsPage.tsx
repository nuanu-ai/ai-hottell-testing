import type { components } from '../../../shared/api';
import { DataLimits, View } from '../../../shared/ui';
import { pageGaps } from '../model/gaps';
import { Commands } from './Commands';
import type { Selection } from './count';
import { ToolsAside } from './ToolsAside';
import { ToolsTable } from './ToolsTable';
import './tools.css';

type Dataset = components['schemas']['AnalyticsDataset'];

type ToolsPageProps = {
  dataset: Pick<Dataset, 'tools' | 'mcp' | 'permissions' | 'commands' | 'gaps'>;
  selection: Selection;
};

// «Инструменты и MCP» (README v5.1): every number is counted over the chosen sessions;
// «Ограничения данных» close the page.
export function ToolsPage({ dataset, selection }: ToolsPageProps) {
  const limits = pageGaps(dataset.gaps);
  return (
    <View>
      <div className="tools-grid">
        <ToolsTable tools={dataset.tools} selection={selection} />
        <ToolsAside mcp={dataset.mcp} permissions={dataset.permissions} selection={selection} />
        <Commands commands={dataset.commands} selection={selection} />
      </div>
      <DataLimits items={limits.items} count={limits.count} />
    </View>
  );
}
