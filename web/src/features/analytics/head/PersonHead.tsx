import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { meQueryOptions, usersQuery } from '../../../shared/api';
import { allPeople, filtersToSearch, useAnalyticsFilters } from '../model/filters';
import './head.css';

// «Аналитика: <Имя> · ← Команда» when ?user= names another person: the select alone does not
// tell the page is about somebody else. The way back keeps the filters but the person (HT-458).
export function PersonHead() {
  const { filters } = useAnalyticsFilters();
  const { data: me } = useQuery(meQueryOptions);
  const { data: users } = useQuery(usersQuery);
  const user = filters.user;
  if (user === undefined || user === allPeople || !me || user === me.id || !users) return null;
  const name = users.items.find((u) => u.id === user)?.name || 'неизвестный человек';
  const search = filtersToSearch({ ...filters, user: undefined });
  return (
    <span className="person-head">
      <b>Аналитика: {name}</b>
      <span className="muted">·</span>
      <Link className="link" to="/team" search={search}>
        ← Команда
      </Link>
    </span>
  );
}
