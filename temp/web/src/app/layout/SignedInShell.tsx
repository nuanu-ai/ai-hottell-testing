import { PersonButton } from '../../features/auth';
import { AppShell } from './AppShell';

// The shell of the closed pages: the signed-in person sits in head-right.
export function SignedInShell() {
  return <AppShell headRight={<PersonButton />} />;
}
