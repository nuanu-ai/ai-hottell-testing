import { PageHead, View } from '../../shared/ui';
import { ThemeSwitch } from '../layout/ThemeSwitch';
import { ChartsSection } from './sections/Charts';
import { ControlsSection } from './sections/Controls';
import { DataSection } from './sections/Data';
import { DiscussSection } from './sections/Discuss';
import { MetricsSection } from './sections/Metrics';
import { OutcomeSection } from './sections/Outcome';
import { PanelsSection } from './sections/Panels';
import { StatesSection } from './sections/States';
import { TabsSection } from './sections/Tabs';
import { TokensSection } from './sections/Tokens';
import { TypographySection } from './sections/Typography';

// The component showcase (dev build only, HT-118): every piece of the design system
// in the current theme, to compare against the v3 dashboard.
export function DesignPage() {
  return (
    <main className="page">
      <View>
        <PageHead
          title="Витрина"
          sub="все части дизайн-системы в текущей теме"
          action={<ThemeSwitch />}
        />
        <TokensSection />
        <TypographySection />
        <ControlsSection />
        <TabsSection />
        <DiscussSection />
        <StatesSection />
        <PanelsSection />
        <DataSection />
        <OutcomeSection />
        <MetricsSection />
        <ChartsSection />
      </View>
    </main>
  );
}
