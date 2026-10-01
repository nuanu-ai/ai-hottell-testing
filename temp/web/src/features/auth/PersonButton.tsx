import { useQuery } from '@tanstack/react-query';

import { meQueryOptions } from '../../shared/api';
import { initials } from './initials';
import { useLogout } from './useLogout';

// The .человек card of deploy's shell.html: a click signs out.
export function PersonButton() {
  const { data: me } = useQuery(meQueryOptions);
  const logout = useLogout();

  if (!me) {
    return null;
  }
  return (
    <button
      className="человек"
      type="button"
      title={`${me.name} — выйти`}
      disabled={logout.isPending}
      onClick={() => {
        logout.mutate();
      }}
    >
      <span className="человек-знак">{initials(me.name)}</span>
      <span className="человек-имя">{me.name}</span>
    </button>
  );
}
