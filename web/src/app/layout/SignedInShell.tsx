import { useMatches } from '@tanstack/react-router';

import { AnalyticsHeadBar } from '../../features/analytics';
import { PersonButton } from '../../features/auth';
import { AppShell } from './AppShell';

// The route id of the analytics tabs (router.tsx, analyticsRoute).
export const analyticsRouteId = '/analytics';

// The shell of the closed pages: the signed-in person sits in head-right, and the analytics tabs
// get the head's second row with their filters.
export function SignedInShell() {
  const onAnalytics = useMatches({
    select: (matches) => matches.some((match) => match.routeId.endsWith(analyticsRouteId)),
  });
  return (
    <AppShell
      headRight={<PersonButton />}
      headBar={onAnalytics ? <AnalyticsHeadBar /> : undefined}
    />
  );
}
