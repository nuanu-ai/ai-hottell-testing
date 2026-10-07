import type { AgentId } from './model';

/** A tab of the page: an agent's, or the folders and history both agents share. */
export type SettingsTab = AgentId | 'shared';

const settingsTabs: readonly SettingsTab[] = ['claude', 'codex', 'shared'];

/** The page's search: the open tab; an unknown one is dropped. */
export function validateTelemetrySettingsSearch(search: Record<string, unknown>): {
  tab?: SettingsTab;
} {
  const tab = settingsTabs.find((id) => id === search.tab);
  return tab ? { tab } : {};
}
