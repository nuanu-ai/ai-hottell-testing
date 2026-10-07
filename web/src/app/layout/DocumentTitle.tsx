import { useMatches } from '@tanstack/react-router';
import { useEffect } from 'react';

import { documentTitle } from './pageTitle';

export function DocumentTitle() {
  const title = useMatches({ select: (matches) => matches.at(-1)?.staticData.title });
  useEffect(() => {
    document.title = documentTitle(title);
  }, [title]);
  return null;
}
