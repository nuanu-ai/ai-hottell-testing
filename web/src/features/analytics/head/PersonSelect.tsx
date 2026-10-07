import { useQuery } from '@tanstack/react-query';

import { meQueryOptions, usersQuery } from '../../../shared/api';
import { allPeople, useAnalyticsFilters } from '../model/filters';

// «Человек»: everyone sees everyone (owner's decision, RBAC later); the signed-in person by default,
// so «Ваше время» and «Ваши сессии» speak of them. Invited people who never signed in are not listed.
export function PersonSelect() {
  const { filters, setFilter } = useAnalyticsFilters();
  const { data: me } = useQuery(meQueryOptions);
  const { data: users } = useQuery(usersQuery);
  const people = (users?.items ?? []).filter((user) => user.status === 'active');
  const value = filters.user ?? me?.id ?? allPeople;

  return (
    <select
      className="seg-select"
      aria-label="Человек"
      value={value}
      onChange={(event) => {
        const chosen = event.target.value;
        // The signed-in person is the default and stays out of the URL.
        setFilter('user', chosen === me?.id ? undefined : chosen);
      }}
    >
      <option value={allPeople}>Все люди</option>
      {people.map((user) => (
        <option key={user.id} value={user.id}>
          {user.name}
        </option>
      ))}
    </select>
  );
}
