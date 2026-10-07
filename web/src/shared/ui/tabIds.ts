// The ids that tie a tab to its panel; Tabs and TabPanel both take them from here.
export function tabIds(tabsId: string, tab: string): { tab: string; panel: string } {
  return { tab: `${tabsId}-tab-${tab}`, panel: `${tabsId}-panel-${tab}` };
}
