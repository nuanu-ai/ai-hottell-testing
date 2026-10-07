import { useQuery } from '@tanstack/react-query';

import { meQueryOptions } from '../../shared/api';
import { initials } from './initials';
import { useLogout } from './useLogout';

// The person button in the app header: a click signs out.
export function PersonButton() {
  const { data: me } = useQuery(meQueryOptions);
  const logout = useLogout();

  if (!me) {
    return null;
  }
  return (
    <button
      className="person"
      type="button"
      title={`${me.name} — выйти`}
      disabled={logout.isPending}
      onClick={() => {
        logout.mutate();
      }}
    >
      <span className="person-mark">{initials(me.name)}</span>
      <span className="person-name">{me.name}</span>
    </button>
  );
}
