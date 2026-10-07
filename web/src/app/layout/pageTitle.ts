import { appTitle } from '../appTitle';

// «<Page> · hottell» in the browser tab; the root page is just «hottell».
export function documentTitle(title: string | undefined): string {
  return !title || title === appTitle ? appTitle : `${title} · ${appTitle}`;
}
